package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

// Signup proves that a person owns an email address before an account exists.
// A six-digit code is emailed and kept in Redis for ten minutes. Each code
// accepts five wrong guesses, then it is deleted: rate limits reset with time
// and differ per device, but this cap belongs to the code itself.
const (
	codeTTL      = 10 * time.Minute
	codeTries    = 5
	codeCooldown = time.Minute
	// Each address receives at most this many codes per hour, so nobody can
	// flood someone else's inbox or reset the guess cap indefinitely.
	codeSendsPerHour = 5
)

var (
	ErrCodeExpired = errors.New("verification code is missing, expired, or used up")
	ErrCodeWrong   = errors.New("verification code is wrong")
)

// WaitError means a new code was requested too soon.
type WaitError struct{ Wait time.Duration }

func (e WaitError) Error() string { return fmt.Sprintf("request another code in %s", e.Wait) }

// WrongCodeError reports how many guesses remain for the current code.
type WrongCodeError struct{ Left int }

func (e WrongCodeError) Error() string { return ErrCodeWrong.Error() }
func (e WrongCodeError) Unwrap() error { return ErrCodeWrong }

// Mailer delivers verification codes.
type Mailer interface {
	SendCode(ctx context.Context, to, code string) error
}

type Signups struct {
	redis  *redis.Client
	secret []byte
	mail   Mailer
}

func NewSignups(client *redis.Client, secret []byte, mail Mailer) *Signups {
	return &Signups{redis: client, secret: secret, mail: mail}
}

// RequestCode emails a new code, replacing any earlier one. It never reads the
// users table, so the response is the same whether or not an account exists.
func (s *Signups) RequestCode(ctx context.Context, email string) error {
	id := s.mac("email", email)
	wait, sends, code := "signup:wait:"+id, "signup:sends:"+id, "signup:code:"+id
	ok, err := s.redis.SetNX(ctx, wait, 1, codeCooldown).Result()
	if err != nil {
		return err
	}
	if !ok {
		return WaitError{s.ttl(ctx, wait, codeCooldown)}
	}
	pipe := s.redis.Pipeline()
	pipe.SetNX(ctx, sends, 0, time.Hour)
	count := pipe.Incr(ctx, sends)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	if count.Val() > codeSendsPerHour {
		return WaitError{s.ttl(ctx, sends, time.Hour)}
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	digits := fmt.Sprintf("%06d", n.Int64())
	if _, err := s.redis.TxPipelined(ctx, func(tx redis.Pipeliner) error {
		tx.Del(ctx, code)
		tx.HSet(ctx, code, "hash", s.mac("code", email+"\x00"+digits), "tries", 0)
		tx.Expire(ctx, code, codeTTL)
		return nil
	}); err != nil {
		return err
	}
	if err := s.mail.SendCode(ctx, email, digits); err != nil {
		// The code never arrived, so let the person ask again right away.
		s.redis.Del(context.WithoutCancel(ctx), wait, code)
		return fmt.Errorf("send verification code: %w", err)
	}
	return nil
}

// checkCode compares the stored hash and counts a wrong guess in one atomic
// step, so concurrent guesses cannot exceed the cap. It returns -1 when no
// code exists, -2 on a match, or the guesses left after a wrong one.
var checkCode = redis.NewScript(`
local stored = redis.call('HGET', KEYS[1], 'hash')
if not stored then return -1 end
if stored == ARGV[1] then return -2 end
local left = tonumber(ARGV[2]) - redis.call('HINCRBY', KEYS[1], 'tries', 1)
if left <= 0 then redis.call('DEL', KEYS[1]) end
return left
`)

// Verify checks a code without using it up, so a failure after verification,
// such as a busy server, can be retried with the same code.
func (s *Signups) Verify(ctx context.Context, email, code string) error {
	key := "signup:code:" + s.mac("email", email)
	left, err := checkCode.Run(ctx, s.redis, []string{key}, s.mac("code", email+"\x00"+code), codeTries).Int()
	switch {
	case err != nil:
		return err
	case left == -2:
		return nil
	case left <= 0:
		return ErrCodeExpired
	default:
		return WrongCodeError{Left: left}
	}
}

// Consume deletes the code once it has been used.
func (s *Signups) Consume(ctx context.Context, email string) error {
	return s.redis.Del(ctx, "signup:code:"+s.mac("email", email)).Err()
}

func (s *Signups) ttl(ctx context.Context, key string, fallback time.Duration) time.Duration {
	if d, err := s.redis.TTL(ctx, key).Result(); err == nil && d > 0 {
		return d
	}
	return fallback
}

// mac keeps email addresses out of Redis key names and makes stored code
// hashes useless without the server secret.
func (s *Signups) mac(purpose, value string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(purpose + "\x00" + value))
	return hex.EncodeToString(m.Sum(nil)[:16])
}
