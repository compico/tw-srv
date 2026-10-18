// memealerts: сервис обмена баллов канала Twitch на мемкоины MemeAlerts.
// Токены Twitch и сессии берёт у сервиса twitch-auth по gRPC.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/authclient"
	"github.com/compico/tw-srv/internal/memealert"
	"github.com/compico/tw-srv/internal/platform/config"
	"github.com/compico/tw-srv/internal/platform/secretbox"
	"github.com/compico/tw-srv/internal/platform/sqlitedb"
	"github.com/compico/tw-srv/internal/platform/view"
	"github.com/compico/tw-srv/internal/support"
	"github.com/compico/tw-srv/internal/twitch"
)

func main() {
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
	supportHTTP := support.NewHTTP(support.NewService(supportRepo, adminID), log)
	if adminID == "" {
		log.Warn("ADMIN_TWITCH_ID не задан: просматривать все заявки поддержки никто не сможет")
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
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	})
	memealert.NewHTTP(svc, authc, memealert.HTTPConfig{
		CookieName:   cookieName,
		CookieSecure: cookieSecure,
		View:         page,
		Extra:        []memealert.RouteRegistrar{supportHTTP.Register},
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
