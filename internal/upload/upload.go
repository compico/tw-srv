// Package upload: загрузка изображений (для markdown-сообщений). Каждый файл принадлежит загрузившему
// пользователю: владелец записан в базе, есть лимиты на размер и количество.
package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
)

const (
	MaxSize          = 5 << 20   // 5 МБ на файл
	MaxFilesPerOwner = 200       // файлов на пользователя
	MaxBytesPerOwner = 200 << 20 // и суммарно 200 МБ
	maxNameLen       = 100
)

var (
	ErrNotFound    = errors.New("файл не найден")
	ErrEmpty       = errors.New("файл пустой")
	ErrTooLarge    = errors.New("файл больше 5 МБ")
	ErrUnsupported = errors.New("поддерживаются только PNG, JPEG, GIF и WebP")
	ErrQuota       = errors.New("превышен лимит загрузок: удалите ненужные файлы")
)

// Допустимые типы определяются по содержимому, а не по имени или заголовку клиента.
// SVG намеренно не поддерживается: в нём может быть скрипт.
var allowedTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type Image struct {
	ID          string `gorm:"primaryKey;size:32"`
	OwnerID     string `gorm:"index;not null;size:32"` // Twitch user id
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}

func (Image) TableName() string { return "uploads" }

// ---------------------------------------------------------------- хранилище на диске

type Store struct{ dir string }

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id[:2], id) }

func (s *Store) write(id string, data []byte) error {
	p := s.path(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Store) read(id string) ([]byte, error) { return os.ReadFile(s.path(id)) }

func (s *Store) remove(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- база

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) (*Repository, error) {
	if err := db.AutoMigrate(&Image{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) create(ctx context.Context, img *Image) error {
	return r.db.WithContext(ctx).Create(img).Error
}

func (r *Repository) get(ctx context.Context, id string) (*Image, error) {
	var img Image
	if err := r.db.WithContext(ctx).First(&img, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &img, nil
}

func (r *Repository) delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&Image{}, "id = ?", id).Error
}

func (r *Repository) listByOwner(ctx context.Context, owner string, limit int) ([]Image, error) {
	var out []Image
	err := r.db.WithContext(ctx).Where("owner_id = ?", owner).Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *Repository) usage(ctx context.Context, owner string) (files, bytes int64, err error) {
	var row struct {
		N     int64
		Bytes int64
	}
	err = r.db.WithContext(ctx).Model(&Image{}).Where("owner_id = ?", owner).
		Select("COUNT(*) AS n, COALESCE(SUM(size), 0) AS bytes").Scan(&row).Error
	return row.N, row.Bytes, err
}

// ---------------------------------------------------------------- сервис

type Service struct {
	repo    *Repository
	store   *Store
	adminID string
}

func NewService(repo *Repository, store *Store, adminID string) *Service {
	return &Service{repo: repo, store: store, adminID: strings.TrimSpace(adminID)}
}

func (s *Service) isAdmin(userID string) bool { return s.adminID != "" && userID == s.adminID }

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" || name == "" {
		return "image"
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		name = string([]rune(name)[:maxNameLen])
	}
	return name
}

// Save проверяет файл и сохраняет его за владельцем ownerID.
func (s *Service) Save(ctx context.Context, ownerID, filename string, data []byte) (*Image, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	ct := http.DetectContentType(head)
	if !allowedTypes[ct] {
		return nil, ErrUnsupported
	}

	files, used, err := s.repo.usage(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	if files >= MaxFilesPerOwner || used+int64(len(data)) > MaxBytesPerOwner {
		return nil, ErrQuota
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	img := &Image{ID: id, OwnerID: ownerID, Name: cleanName(filename), ContentType: ct, Size: int64(len(data))}
	if err := s.store.write(id, data); err != nil {
		return nil, err
	}
	if err := s.repo.create(ctx, img); err != nil {
		_ = s.store.remove(id)
		return nil, err
	}
	return img, nil
}

// Open возвращает файл по id. Доступ только вошедшим пользователям (проверяет HTTP-слой);
// id случайный 128-битный, поэтому ссылку нельзя подобрать.
func (s *Service) Open(ctx context.Context, id string) (*Image, []byte, error) {
	if !validID(id) {
		return nil, nil, ErrNotFound
	}
	img, err := s.repo.get(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	data, err := s.store.read(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	return img, data, nil
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Image, error) {
	return s.repo.listByOwner(ctx, ownerID, MaxFilesPerOwner)
}

// Delete удаляет файл; может владелец или администратор.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	img, err := s.repo.get(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if img.OwnerID != userID && !s.isAdmin(userID) {
		return ErrNotFound
	}
	if err := s.repo.delete(ctx, id); err != nil {
		return err
	}
	return s.store.remove(id)
}
