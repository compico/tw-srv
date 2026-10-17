// twitch-auth: сервис авторизации Twitch: вход пользователей, сессии и обновление токенов.
// Наружу отдаёт /auth/* по HTTP, внутрь (docker-сеть): gRPC AuthService.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"google.golang.org/grpc"

	"github.com/compico/overlaycomposer/internal/auth"
	"github.com/compico/overlaycomposer/internal/authpb"
	"github.com/compico/overlaycomposer/internal/platform/config"
	"github.com/compico/overlaycomposer/internal/platform/secretbox"
	"github.com/compico/overlaycomposer/internal/platform/sqlitedb"
	"github.com/compico/overlaycomposer/internal/twitch"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", "twitch-auth")

	env := &config.Env{}
	httpAddr := env.String("HTTP_ADDR", ":8081")
	grpcAddr := env.String("GRPC_ADDR", ":9090")
	publicURL := strings.TrimRight(env.Require("PUBLIC_URL"), "/")
	clientID := env.Require("TWITCH_CLIENT_ID")
	clientSecret := env.Require("TWITCH_CLIENT_SECRET")
	dbPath := env.String("DB_PATH", "data/auth.db")
	encKey := env.Require("TOKEN_ENCRYPTION_KEY")
	grpcSecret := env.Require("GRPC_SHARED_SECRET")
	cookieName := env.String("SESSION_COOKIE", "oc_session")
	cookieSecure := env.Bool("COOKIE_SECURE", true)
	sessionTTL := env.Duration("SESSION_TTL", 30*24*time.Hour)
	scopes := env.List("TWITCH_SCOPES", []string{"channel:manage:redemptions"})
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
	repo, err := auth.NewRepository(db)
	if err != nil {
		return err
	}

	tw := twitch.NewOAuth(clientID, clientSecret, publicURL+"/auth/twitch/callback")
	svc := auth.NewService(repo, tw, box, auth.Config{Scopes: scopes, SessionTTL: sessionTTL}, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go svc.Run(ctx)

	// gRPC
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(auth.UnaryAuthInterceptor(grpcSecret)))
	authpb.RegisterAuthServiceServer(grpcSrv, auth.NewGRPCServer(svc))
	go func() {
		log.Info("grpc listening", "addr", grpcAddr)
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error("grpc serve", "err", err)
			stop()
		}
	}()

	// HTTP
	app := fiber.New(fiber.Config{
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	})
	auth.NewHTTP(svc, auth.HTTPConfig{CookieName: cookieName, CookieSecure: cookieSecure, SessionTTL: sessionTTL}, log).Register(app)
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
	grpcSrv.GracefulStop()
	return app.ShutdownWithContext(shutdownCtx)
}
