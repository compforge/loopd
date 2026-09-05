package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

// HumanIdentity may resolve an authenticated principal from a trusted host.
// Quick Start uses an opaque HttpOnly browser cookie; submitted actor keys are
// never credentials. The principal is bound to the initial task input.
type HumanIdentity func(context.Context, *hertzapp.RequestContext) (string, error)

func browserIdentity(_ context.Context, r *hertzapp.RequestContext) (string, error) {
	token := string(r.Cookie("loopd-human"))
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		token = hex.EncodeToString(raw)
		cookie := "loopd-human=" + token + "; Path=/; HttpOnly; SameSite=Strict; Max-Age=31536000"
		if string(r.URI().Scheme()) == "https" {
			cookie += "; Secure"
		}
		r.Response.Header.Add("Set-Cookie", cookie)
	}
	hash := sha256.Sum256([]byte(token))
	return "human-" + hex.EncodeToString(hash[:]), nil
}
func (s *Server) identity(ctx context.Context, r *hertzapp.RequestContext) (string, error) {
	if s.HumanIdentity != nil {
		return s.HumanIdentity(ctx, r)
	}
	return browserIdentity(ctx, r)
}
func (s *Server) createHuman(ctx context.Context, r *hertzapp.RequestContext) error {
	if s.Human == nil {
		return service.ErrUnavailable
	}
	var input loopd.HumanRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	if input.ConversationID != "" && input.ConversationID != r.Param("conversation_id") {
		return service.ErrInvalid
	}
	input.ConversationID = r.Param("conversation_id")
	result, err := s.Human.Create(ctx, input)
	if err != nil {
		return err
	}
	r.JSON(200, result)
	return nil
}
func (s *Server) getHuman(ctx context.Context, r *hertzapp.RequestContext) error {
	if s.Human == nil {
		return service.ErrUnavailable
	}
	result, err := s.Human.Get(ctx, r.Param("message_id"))
	if err != nil {
		return err
	}
	r.JSON(200, result)
	return nil
}
func (s *Server) replyHuman(ctx context.Context, r *hertzapp.RequestContext) error {
	if s.Human == nil {
		return service.ErrUnavailable
	}
	// JSON content type plus a same-origin cookie prevents form-based CSRF.
	if string(r.ContentType()) != "application/json" {
		return fmt.Errorf("%w: application/json required", service.ErrInvalid)
	}
	actor, err := s.identity(ctx, r)
	if err != nil {
		return err
	}
	if actor == "" {
		return repo.ErrForbidden
	}
	var input loopd.HumanReply
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	result, err := s.Human.Reply(ctx, r.Param("conversation_id"), actor, input)
	if err != nil {
		return err
	}
	r.JSON(200, result)
	return nil
}
