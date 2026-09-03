package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const workflowFileArtifactMetadataBodyLimit int64 = 16 * 1024

type workflowFileArtifactController struct {
	service pro_interfaces.WorkflowFileArtifactServiceFacade
}

var _ pro_interfaces.WorkflowFileArtifactController = (*workflowFileArtifactController)(nil)

func NewWorkflowFileArtifactController(service pro_interfaces.WorkflowFileArtifactServiceFacade) pro_interfaces.WorkflowFileArtifactController {
	return &workflowFileArtifactController{service: service}
}

func (controller *workflowFileArtifactController) BeginWorkflowFileArtifact(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	var upload db.WorkflowFileArtifactUpload
	if !decodeWorkflowFileArtifactJSON(w, r, &upload) {
		return
	}
	artifact, err := controller.service.BeginWorkflowFileArtifact(r.Context(), project.ID, run.ID, upload, user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, artifact)
}

func (controller *workflowFileArtifactController) AppendWorkflowFileArtifact(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	artifactID, revision, offset, ok := workflowFileArtifactMutationParameters(w, r, true)
	if !ok {
		return
	}
	if r.ContentLength == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.ContentLength > int64(db.MaxWorkflowFileArtifactChunkBytes) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	reader := http.MaxBytesReader(w, r.Body, int64(db.MaxWorkflowFileArtifactChunkBytes))
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if len(content) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	artifact, err := controller.service.AppendWorkflowFileArtifact(r.Context(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: project.ID, WorkflowRunID: run.ID, ArtifactID: artifactID,
		ExpectedRevision: revision, OffsetBytes: offset, Data: content,
	}, user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifact)
}

func (controller *workflowFileArtifactController) FinalizeWorkflowFileArtifact(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	artifactID, revision, _, ok := workflowFileArtifactMutationParameters(w, r, false)
	if !ok {
		return
	}
	artifact, err := controller.service.FinalizeWorkflowFileArtifact(r.Context(), pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: project.ID, WorkflowRunID: run.ID, ArtifactID: artifactID, ExpectedRevision: revision,
	}, user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifact)
}

func (controller *workflowFileArtifactController) GetWorkflowFileArtifacts(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			helpers.WriteJSON(w, http.StatusOK, []db.WorkflowFileArtifactMetadata{})
		}
		return
	}
	artifacts, err := controller.service.GetWorkflowFileArtifacts(r.Context(), project.ID, run.ID, helpers.QueryParams(r.URL), user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifacts)
}

func (controller *workflowFileArtifactController) GetWorkflowFileArtifact(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	artifactID, ok := workflowFileArtifactID(w, r)
	if !ok {
		return
	}
	artifact, err := controller.service.GetWorkflowFileArtifact(r.Context(), project.ID, run.ID, artifactID, user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifact)
}

func (controller *workflowFileArtifactController) DownloadWorkflowFileArtifact(w http.ResponseWriter, r *http.Request) {
	project, run, user, ok := workflowFileArtifactRequestContext(w, r)
	if !ok || controller.service == nil {
		if ok {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	artifactID, ok := workflowFileArtifactID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodHead || strings.TrimSpace(r.Header.Get("Range")) != "" {
		artifact, err := controller.service.GetWorkflowFileArtifact(r.Context(), project.ID, run.ID, artifactID, user)
		if err != nil {
			writeWorkflowFileArtifactError(w, err)
			return
		}
		setWorkflowFileArtifactDownloadHeaders(w.Header(), artifact)
		if strings.TrimSpace(r.Header.Get("Range")) != "" {
			w.Header().Del("Content-Length")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", artifact.SizeBytes))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	download, err := controller.service.AcquireWorkflowFileArtifactDownload(r.Context(), project.ID, run.ID, artifactID, user)
	if err != nil {
		writeWorkflowFileArtifactError(w, err)
		return
	}
	defer func() {
		if releaseErr := controller.service.ReleaseWorkflowFileArtifactDownload(download); releaseErr != nil {
			log.WithError(releaseErr).Warn("failed to release workflow file artifact download lease")
		}
	}()
	if err = http.NewResponseController(w).SetWriteDeadline(download.Deadline); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	setWorkflowFileArtifactDownloadHeaders(w.Header(), download.Metadata)
	w.WriteHeader(http.StatusOK)
	if _, err = controller.service.StreamWorkflowFileArtifactDownload(r.Context(), download, w); err != nil {
		log.WithError(err).Warn("workflow file artifact download ended before completion")
	}
}

func workflowFileArtifactRequestContext(w http.ResponseWriter, r *http.Request) (db.Project, db.WorkflowRun, *db.User, bool) {
	projectValue, projectOK := helpers.GetOkFromContext(r, "project")
	runValue, runOK := helpers.GetOkFromContext(r, "workflow_run")
	userValue, userOK := helpers.GetOkFromContext(r, "user")
	project, validProject := projectValue.(db.Project)
	run, validRun := runValue.(db.WorkflowRun)
	user, validUser := userValue.(*db.User)
	if !projectOK || !runOK || !userOK || !validProject || !validRun || !validUser || user == nil || user.ID < 1 ||
		project.ID < 1 || run.ID < 1 || run.ProjectID != project.ID {
		w.WriteHeader(http.StatusNotFound)
		return db.Project{}, db.WorkflowRun{}, nil, false
	}
	return project, run, user, true
}

func decodeWorkflowFileArtifactJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	reader := http.MaxBytesReader(w, r.Body, workflowFileArtifactMetadataBodyLimit)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func workflowFileArtifactMutationParameters(w http.ResponseWriter, r *http.Request, withOffset bool) (int, int, int64, bool) {
	artifactID, ok := workflowFileArtifactID(w, r)
	if !ok {
		return 0, 0, 0, false
	}
	allowed := map[string]bool{"revision": true}
	if withOffset {
		allowed["offset"] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return 0, 0, 0, false
		}
	}
	revision, err := strconv.Atoi(r.URL.Query().Get("revision"))
	if err != nil || revision < 1 {
		w.WriteHeader(http.StatusBadRequest)
		return 0, 0, 0, false
	}
	var offset int64
	if withOffset {
		offset, err = strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil || offset < 0 {
			w.WriteHeader(http.StatusBadRequest)
			return 0, 0, 0, false
		}
	}
	return artifactID, revision, offset, true
}

func workflowFileArtifactID(w http.ResponseWriter, r *http.Request) (int, bool) {
	artifactID, err := strconv.Atoi(mux.Vars(r)["artifact_id"])
	if err != nil || artifactID < 1 {
		w.WriteHeader(http.StatusBadRequest)
		return 0, false
	}
	return artifactID, true
}

func setWorkflowFileArtifactDownloadHeaders(header http.Header, artifact db.WorkflowFileArtifactMetadata) {
	header.Set("Content-Type", artifact.MediaType)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename}))
	header.Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	header.Set("X-Checksum-SHA256", artifact.SHA256)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "private, no-store")
	header.Set("Accept-Ranges", "none")
}

func writeWorkflowFileArtifactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, pro_interfaces.ErrWorkflowPermissionDenied), errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactNotAvailable):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrInvalidOperation):
		w.WriteHeader(http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactConflict):
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactQuotaExceeded):
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	case errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactIncomplete), errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactChecksum):
		w.WriteHeader(http.StatusUnprocessableEntity)
	default:
		helpers.WriteError(w, err)
	}
}
