package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Material Folder Handlers ───────────────────────────

// handleFolderList lists all material folders for the authenticated user.
func (s *Server) handleFolderList(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	folders, err := s.kbMgr.ListFolders(r.Context(), userID)
	if err != nil {
		slog.Warn("list folders failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list folders")
		return
	}

	response.OK(w, map[string]any{
		"folders": folders,
		"total":   len(folders),
	})
}

// handleFolderCreate creates a new material folder.
func (s *Server) handleFolderCreate(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		Name        string `json:"name"`
		ParentID    string `json:"parent_id"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Name == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "folder name is required")
		return
	}

	folder, err := s.kbMgr.CreateFolder(r.Context(), userID, req.Name, req.ParentID, req.Description)
	if err != nil {
		slog.Warn("create folder failed", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create folder")
		return
	}

	response.Created(w, folder)
}

// handleFolderUpdate updates a material folder's name and/or description.
func (s *Server) handleFolderUpdate(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	folderID := chi.URLParam(r, "id")
	if folderID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "folder ID is required")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	folder, err := s.kbMgr.UpdateFolder(r.Context(), userID, folderID, req.Name, req.Description)
	if err != nil {
		slog.Warn("update folder failed", "error", err, "folder_id", folderID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update folder")
		return
	}

	response.OK(w, folder)
}

// handleFolderDelete deletes a material folder.
func (s *Server) handleFolderDelete(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	folderID := chi.URLParam(r, "id")
	if folderID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "folder ID is required")
		return
	}

	if err := s.kbMgr.DeleteFolder(r.Context(), userID, folderID); err != nil {
		slog.Warn("delete folder failed", "error", err, "folder_id", folderID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete folder")
		return
	}

	response.OK(w, map[string]any{"message": "folder deleted", "id": folderID})
}

// handleMaterialMove moves a material to a different folder.
func (s *Server) handleMaterialMove(w http.ResponseWriter, r *http.Request) {
	if s.kbMgr == nil {
		response.Err(w, http.StatusServiceUnavailable, "kb_not_configured", "Knowledge base is not configured")
		return
	}

	userID := s.getUserID(r)
	if userID == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	materialID := chi.URLParam(r, "id")
	if materialID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "material ID is required")
		return
	}

	var req struct {
		FolderID string `json:"folder_id"` // empty = root
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if err := s.kbMgr.MoveMaterialToFolder(r.Context(), userID, materialID, req.FolderID); err != nil {
		slog.Warn("move material failed", "error", err, "material_id", materialID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to move material")
		return
	}

	response.OK(w, map[string]any{"message": "material moved", "id": materialID, "folder_id": req.FolderID})
}
