package operator

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

func sessionToken(password string, expiry int64) string {
	value := strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, []byte(password))
	_, _ = mac.Write([]byte("operator-session:" + value))
	return value + "." + hex.EncodeToString(mac.Sum(nil))
}
func validSession(password, token string) bool {
	parts := strings.Split(token, ".")
	if password == "" || len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiry <= time.Now().Unix() || expiry > time.Now().Add(12*time.Hour).Unix()+5 {
		return false
	}
	return hmac.Equal([]byte(token), []byte(sessionToken(password, expiry)))
}
func (s *Service) resolvePublicID(ctx context.Context, id string) (string, error) {
	list, err := s.accounts(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range list {
		if publicID(a.ID) == id {
			return a.ID, nil
		}
	}
	return "", errors.New("account not found")
}
