package handlers

import (
	"sumi/config"
	"sumi/internal/services"
)

type Handler struct {
	S   *services.Service
	Cfg *config.Config
}

func NewHandler(s *services.Service, cfg *config.Config) *Handler {
	return &Handler{
		S:   s,
		Cfg: cfg,
	}
}
