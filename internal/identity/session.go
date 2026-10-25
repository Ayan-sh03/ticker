package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"ticker/db/queries"
)

var (
	ErrCredentials = errors.New("invalid credentials")
	ErrSession     = errors.New("session is missing or expired")
	ErrBusy        = errors.New("password capacity exceeded")
	ErrAccount     = errors.New("an account already uses this email")
)

const (
	passwordCost = 12
	// A login waits this long for a hashing slot before it is turned away.
	passwordWait = time.Second
)

type User struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	TenantIDs   []string `json:"tenant_ids"`
}

type Session struct {
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Service struct {
	pool        *pgxpool.Pool
	dummyHash   []byte
	verifySlots chan struct{}
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy credential for missing accounts"), passwordCost)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, dummyHash: dummy, verifySlots: make(chan struct{}, passwordSlots())}, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 72 {
		return "", errors.New("password must contain 12–72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	return string(hash), err
}

func PasswordMatches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// passwordSlots leaves at least half the CPUs for other requests: each bcrypt
// check keeps one CPU busy, so running more of them at once only slows every
// request on the instance without finishing logins sooner.
func passwordSlots() int {
	return (runtime.GOMAXPROCS(0) + 1) / 2
}

// passwordSlot bounds concurrent bcrypt work independently of the database pool.
// A short wait lets a few logins arriving together take turns instead of failing.
func (s *Service) passwordSlot(ctx context.Context) (func(), error) {
	timer := time.NewTimer(passwordWait)
	defer timer.Stop()
	select {
	case s.verifySlots <- struct{}{}:
		return func() { <-s.verifySlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrBusy
	}
}

func (s *Service) Login(ctx context.Context, email, password, previousToken string) (Session, string, error) {
	release, err := s.passwordSlot(ctx)
	if err != nil {
		return Session{}, "", err
	}
	defer release()
	var session Session
	var passwordHash string
	err = s.pool.QueryRow(ctx, queries.GetLoginUser, email).Scan(
		&session.User.ID, &session.User.Email, &session.User.DisplayName, &passwordHash, &session.User.TenantIDs)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, "", err
	}
	hash := []byte(passwordHash)
	if passwordHash == "" {
		hash = s.dummyHash
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if err != nil || passwordHash == "" || !match {
		return Session{}, "", ErrCredentials
	}
	return s.startSession(ctx, session, previousToken, nil)
}

// CreateAccount creates a customer account and signs it in. The caller must
// have verified that the requester owns the email address.
func (s *Service) CreateAccount(ctx context.Context, email, displayName, password, previousToken string) (Session, string, error) {
	release, err := s.passwordSlot(ctx)
	if err != nil {
		return Session{}, "", err
	}
	passwordHash, err := HashPassword(password)
	release()
	if err != nil {
		return Session{}, "", err
	}
	session := Session{User: User{Email: email, DisplayName: displayName, TenantIDs: []string{}}}
	return s.startSession(ctx, session, previousToken, func(tx pgx.Tx, session *Session) error {
		err := tx.QueryRow(ctx, queries.CreateUser, email, displayName, passwordHash).Scan(&session.User.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccount
		}
		return err
	})
}

// startSession issues a new session token. A successful login or signup
// replaces this browser's previous session atomically. before runs first in
// the same transaction and may fill in session.User.ID.
func (s *Service) startSession(ctx context.Context, session Session, previousToken string, before func(pgx.Tx, *Session) error) (Session, string, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, "", fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tokenHash, _ := hashToken(token)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, "", err
	}
	defer tx.Rollback(context.Background())
	if before != nil {
		if err := before(tx, &session); err != nil {
			return Session{}, "", err
		}
	}
	if previousHash, valid := hashToken(previousToken); valid {
		if _, err := tx.Exec(ctx, queries.DeleteSession, previousHash); err != nil {
			return Session{}, "", err
		}
	}
	if err := tx.QueryRow(ctx, queries.CreateSession, session.User.ID, tokenHash).Scan(&session.ExpiresAt); err != nil {
		return Session{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func (s *Service) Lookup(ctx context.Context, token string) (Session, error) {
	hash, valid := hashToken(token)
	if !valid {
		return Session{}, ErrSession
	}
	var session Session
	err := s.pool.QueryRow(ctx, queries.GetSession, hash).Scan(
		&session.User.ID, &session.User.Email, &session.User.DisplayName, &session.User.TenantIDs, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSession
	}
	return session, err
}

func (s *Service) Revoke(ctx context.Context, token string) error {
	hash, valid := hashToken(token)
	if !valid {
		return nil
	}
	_, err := s.pool.Exec(ctx, queries.DeleteSession, hash)
	return err
}

func hashToken(token string) ([]byte, bool) {
	if len(token) != 43 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], true
}
