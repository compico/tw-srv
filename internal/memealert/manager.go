package memealert

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/compico/overlaycomposer/internal/twitch"
)

// Manager держит по одному слушателю EventSub на стримера с включённой наградой.
type Manager struct {
	svc  *Service
	es   *twitch.EventSub
	log  *slog.Logger
	root context.Context

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewManager: root живёт всё время работы процесса; заявки обрабатываются в его контексте,
// чтобы переподключение слушателя не обрывало выдачу бонуса на полпути.
func NewManager(root context.Context, svc *Service, es *twitch.EventSub, log *slog.Logger) *Manager {
	return &Manager{svc: svc, es: es, log: log, root: root, running: map[string]context.CancelFunc{}}
}

// Start (пере)запускает слушатель по актуальным настройкам награды.
// Если награды нет, она выключена или не создана на Twitch, слушатель просто останавливается.
func (m *Manager) Start(userID string) {
	rw, err := m.svc.repo.GetReward(m.root, userID)

	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, ok := m.running[userID]; ok {
		cancel()
		delete(m.running, userID)
	}
	if err != nil || rw.TwitchRewardID == "" || !rw.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(m.root)
	m.running[userID] = cancel
	go m.run(ctx, userID, rw.TwitchRewardID)
}

func (m *Manager) Stop(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.running[userID]; ok {
		cancel()
		delete(m.running, userID)
	}
}

func (m *Manager) run(ctx context.Context, userID, rewardID string) {
	m.log.Info("listener started", "user", userID, "reward", rewardID)
	err := m.es.Listen(ctx, m.svc.tokens, userID, rewardID,
		func(ev twitch.Redemption) {
			// Не блокируем цикл чтения сокета: иначе пропустим ping/keepalive.
			go m.svc.HandleRedemption(m.root, ev)
		},
		func() {
			go m.svc.CatchUp(m.root, userID)
		},
	)
	switch {
	case errors.Is(err, twitch.ErrReauthRequired):
		m.log.Warn("twitch access revoked, listener stopped", "user", userID)
		m.svc.MarkTwitchReauth(m.root, userID)
	case err != nil && !errors.Is(err, context.Canceled):
		m.log.Error("listener stopped", "user", userID, "err", err)
	default:
		m.log.Info("listener stopped", "user", userID)
	}
}
