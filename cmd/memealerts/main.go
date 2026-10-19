// memealerts: сервис обмена баллов канала Twitch на мемкоины MemeAlerts.
// Токены Twitch и сессии берёт у сервиса twitch-auth по gRPC.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/authclient"
	"github.com/compico/tw-srv/internal/memealert"
	"github.com/compico/tw-srv/internal/platform/config"
	"github.com/compico/tw-srv/internal/platform/secretbox"
	"github.com/compico/tw-srv/internal/platform/slowreq"
	"github.com/compico/tw-srv/internal/platform/sqlitedb"
	"github.com/compico/tw-srv/internal/platform/view"
	"github.com/compico/tw-srv/internal/platform/webguard"
	"github.com/compico/tw-srv/internal/push"
	"github.com/compico/tw-srv/internal/support"
	"github.com/compico/tw-srv/internal/twitch"
	"github.com/compico/tw-srv/internal/upload"
)

func main() {
	// `app vapid-keys` печатает новую пару VAPID-ключей для push-уведомлений и выходит.
	if len(os.Args) > 1 && os.Args[1] == "vapid-keys" {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			slog.Error("generate vapid keys", "err", err)
			os.Exit(1)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", "memealerts")

	env := &config.Env{}
	httpAddr := env.String("HTTP_ADDR", ":8080")
	publicURL := strings.TrimRight(env.Require("PUBLIC_URL"), "/")
	authAddr := env.String("AUTH_GRPC_ADDR", "twitch-auth:9090")
	grpcSecret := env.Require("GRPC_SHARED_SECRET")
	maClientID := env.Require("MEMEALERTS_CLIENT_ID")
	maClientSecret := env.Require("MEMEALERTS_CLIENT_SECRET")
	dbPath := env.String("DB_PATH", "data/memealerts.db")
	encKey := env.Require("TOKEN_ENCRYPTION_KEY")
	cookieName := env.String("SESSION_COOKIE", "oc_session")
	cookieSecure := env.Bool("COOKIE_SECURE", true)
	// Twitch user id администратора поддержки. Пусто: доступа ко всем заявкам нет ни у кого.
	adminID := env.String("ADMIN_TWITCH_ID", "")
	uploadsDir := env.String("UPLOADS_DIR", "data/uploads")
	// Push-уведомления администратору (необязательно): ключи создаёт `app vapid-keys`.
	vapidPublic := env.String("VAPID_PUBLIC_KEY", "")
	vapidPrivate := env.String("VAPID_PRIVATE_KEY", "")
	vapidSubject := env.String("VAPID_SUBJECT", "") // mailto:... или https://...; по умолчанию адрес сайта
	// Источники, которым разрешены запросы к API с cookie (CORS). Адрес сайта (PUBLIC_URL) разрешён всегда;
	// переменная нужна, например, для локальной разработки (http://localhost:8080).
	extraOrigins := env.List("CORS_ALLOWED_ORIGINS", nil)
	dev := env.Bool("DEV", false)
	viteURL := env.String("VITE_DEV_URL", "http://localhost:5173")
	if err := env.Err(); err != nil {
		return err
	}

	box, err := secretbox.New(encKey)
	if err != nil {
		return err
	}
	db, err := sqlitedb.Open(dbPath)
	if err != nil {
		return err
	}
	repo, err := memealert.NewRepository(db)
	if err != nil {
		return err
	}

	supportRepo, err := support.NewRepository(db)
	if err != nil {
		return err
	}
	supportSvc := support.NewService(supportRepo, adminID)
	supportHTTP := support.NewHTTP(supportSvc, log)
	if adminID == "" {
		log.Warn("ADMIN_TWITCH_ID не задан: просматривать все заявки поддержки никто не сможет")
	}

	uploadRepo, err := upload.NewRepository(db)
	if err != nil {
		return err
	}
	uploadStore, err := upload.NewStore(uploadsDir)
	if err != nil {
		return err
	}
	guard := webguard.New(webguard.Config{
		Secret:     encKey,
		CookieName: cookieName,
		Origins:    append([]string{publicURL}, extraOrigins...),
	})
	uploadHTTP := upload.NewHTTP(upload.NewService(uploadRepo, uploadStore, adminID), guard, log)

	pushRepo, err := push.NewRepository(db)
	if err != nil {
		return err
	}
	if vapidSubject == "" {
		vapidSubject = publicURL
	}
	pushSvc := push.NewService(pushRepo, push.Config{
		PublicKey: vapidPublic, PrivateKey: vapidPrivate, Subject: vapidSubject, AdminID: adminID,
	}, log)
	supportSvc.SetNotifier(pushSvc)
	pushHTTP := push.NewHTTP(pushSvc, guard, log)
	if !pushSvc.Enabled() {
		log.Info("push-уведомления выключены: задайте VAPID_PUBLIC_KEY и VAPID_PRIVATE_KEY (app vapid-keys)")
	}

	authc, err := authclient.Dial(authAddr, grpcSecret)
	if err != nil {
		return err
	}
	defer authc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	helix := twitch.NewHelix()
	es := twitch.NewEventSub(helix, log)
	svc := memealert.NewService(repo, memealert.NewAPIClient(maClientID, maClientSecret), box, authc, helix, publicURL, log)
	manager := memealert.NewManager(ctx, svc, es, log)
	svc.SetListeners(manager)

	go svc.RunTokenRefresher(ctx)

	if err := svc.Recover(ctx); err != nil {
		return err
	}

	page, err := view.New(view.Config{Dev: dev, DevURL: viteURL, Title: "Мемкоины за баллы"})
	if err != nil {
		return err
	}

	app := fiber.New(fiber.Config{
		ReadTimeout:  60 * time.Second, // с запасом на загрузку файла до 5 МБ
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		BodyLimit:    upload.MaxSize + 1<<20, // файл до 5 МБ плюс служебные поля multipart
	})
	app.Use(slowreq.New(log, slowreq.DefaultThreshold))
	memealert.NewHTTP(svc, authc, memealert.HTTPConfig{
		CookieName:   cookieName,
		CookieSecure: cookieSecure,
		View:         page,
		Extra:        []memealert.RouteRegistrar{supportHTTP.Register, uploadHTTP.Register, pushHTTP.Register},
	}, log).Register(app)

	go func() {
		log.Info("http listening", "addr", httpAddr)
		if err := app.Listen(httpAddr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return app.ShutdownWithContext(shutdownCtx)
}
