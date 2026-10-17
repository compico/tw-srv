package auth

import (
	"context"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/compico/tw-srv/internal/authpb"
	"github.com/compico/tw-srv/internal/twitch"
)

// GRPCServer: внутренний API сервиса twitch-auth для сервиса memealerts.
type GRPCServer struct {
	authpb.UnimplementedAuthServiceServer
	svc *Service
}

func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

func (g *GRPCServer) ValidateSession(ctx context.Context, req *authpb.ValidateSessionRequest) (*authpb.ValidateSessionResponse, error) {
	u, err := g.svc.ValidateSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, mapError(err)
	}
	return &authpb.ValidateSessionResponse{
		TwitchUserId: u.ID,
		Login:        u.Login,
		DisplayName:  u.DisplayName,
		AvatarUrl:    u.AvatarURL,
	}, nil
}

func (g *GRPCServer) GetAccessToken(ctx context.Context, req *authpb.GetAccessTokenRequest) (*authpb.GetAccessTokenResponse, error) {
	if req.GetTwitchUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "twitch_user_id is required")
	}
	grant, err := g.svc.AccessToken(ctx, req.GetTwitchUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &authpb.GetAccessTokenResponse{
		AccessToken:   grant.AccessToken,
		ClientId:      grant.ClientID,
		ExpiresAtUnix: grant.ExpiresAt.Unix(),
		Scopes:        grant.Scopes,
	}, nil
}

func (g *GRPCServer) Logout(ctx context.Context, req *authpb.LogoutRequest) (*authpb.LogoutResponse, error) {
	if err := g.svc.Logout(ctx, req.GetSessionId()); err != nil {
		return nil, mapError(err)
	}
	return &authpb.LogoutResponse{}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, twitch.ErrNoSession):
		return status.Error(codes.Unauthenticated, "no valid session")
	case errors.Is(err, twitch.ErrReauthRequired):
		return status.Error(codes.FailedPrecondition, "reauth_required")
	case errors.Is(err, twitch.ErrUnknownUser):
		return status.Error(codes.NotFound, "unknown user")
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

// UnaryAuthInterceptor проверяет общий секрет в metadata "authorization: Bearer <secret>".
// gRPC-порт не публикуется наружу, секрет нужен как второй рубеж защиты.
func UnaryAuthInterceptor(secret string) grpc.UnaryServerInterceptor {
	want := []byte("Bearer " + secret)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get("authorization")
		if len(vals) == 0 || subtle.ConstantTimeCompare([]byte(vals[0]), want) != 1 {
			// PermissionDenied, а не Unauthenticated: Unauthenticated означает «нет сессии пользователя».
			return nil, status.Error(codes.PermissionDenied, "bad service credentials")
		}
		return handler(ctx, req)
	}
}
