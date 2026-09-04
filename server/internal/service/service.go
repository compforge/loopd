// Package service owns loop-server's Conversation and Message rules.
package service

import (
	"errors"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
)

var (
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
)

type Repository interface {
	repo.ConversationRepository
	repo.MessageRepository
}

type Service struct {
	repo       Repository
	responders []loopd.Responder
}

func New(repository Repository, responders []loopd.Responder) *Service {
	return &Service{
		repo:       repository,
		responders: append([]loopd.Responder(nil), responders...),
	}
}

func (service *Service) Responders() []loopd.Responder {
	return append([]loopd.Responder(nil), service.responders...)
}
