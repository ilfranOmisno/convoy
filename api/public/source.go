package public

import (
	"fmt"
	"net/http"

	"github.com/frain-dev/convoy"

	"github.com/frain-dev/convoy/pkg/log"

	"github.com/frain-dev/convoy/api/models"
	"github.com/frain-dev/convoy/database/postgres"
	"github.com/frain-dev/convoy/datastore"
	m "github.com/frain-dev/convoy/internal/pkg/middleware"
	"github.com/frain-dev/convoy/services"
	"github.com/frain-dev/convoy/util"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func createSourceService(a *PublicHandler) *services.SourceService {
	sourceRepo := postgres.NewSourceRepo(a.A.DB)

	return services.NewSourceService(sourceRepo, a.A.Cache)
}

// CreateSource
// @Summary Create a source
// @Description This endpoint creates a source
// @Tags Sources
// @Accept  json
// @Produce  json
// @Param projectID path string true "Project ID"
// @Param source body models.Source true "Source Details"
// @Success 200 {object} util.ServerResponse{data=models.Source}
// @Failure 400,401,404 {object} util.ServerResponse{data=Stub}
// @Security ApiKeyAuth
// @Router /v1/projects/{projectID}/sources [post]
func (a *PublicHandler) CreateSource(w http.ResponseWriter, r *http.Request) {
	var newSource models.Source
	if err := util.ReadJSON(r, &newSource); err != nil {
		_ = render.Render(w, r, util.NewErrorResponse(err.Error(), http.StatusBadRequest))
		return
	}

	project, err := a.retrieveProject(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	sourceService := createSourceService(a)
	source, err := sourceService.CreateSource(r.Context(), &newSource, project)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	org, err := postgres.NewOrgRepo(a.A.DB).FetchOrganisationByID(r.Context(), project.OrganisationID)
	if err != nil {
		log.FromContext(r.Context()).WithError(err).Error("failed to find organisation by id")
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	baseUrl, err := a.retrieveHost()
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	fillSourceURL(source, baseUrl, org.CustomDomain.ValueOrZero())
	_ = render.Render(w, r, util.NewServerResponse("Source created successfully", source, http.StatusCreated))
}

// GetSourceByID
// @Summary Retrieve a source
// @Description This endpoint retrieves a source by its id
// @Tags Sources
// @Accept  json
// @Produce  json
// @Param projectID path string true "Project ID"
// @Param sourceID path string true "Source ID"
// @Success 200 {object} util.ServerResponse{data=models.Source}
// @Failure 400,401,404 {object} util.ServerResponse{data=Stub}
// @Security ApiKeyAuth
// @Router /v1/projects/{projectID}/sources/{sourceID} [get]
func (a *PublicHandler) GetSourceByID(w http.ResponseWriter, r *http.Request) {
	project, err := a.retrieveProject(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	source, err := postgres.NewSourceRepo(a.A.DB).FindSourceByID(r.Context(), project.UID, chi.URLParam(r, "sourceID"))
	if err != nil {
		if err == datastore.ErrSourceNotFound {
			_ = render.Render(w, r, util.NewErrorResponse(err.Error(), http.StatusNotFound))
			return
		}

		_ = render.Render(w, r, util.NewErrorResponse("error retrieving source", http.StatusBadRequest))
		return
	}

	org, err := postgres.NewOrgRepo(a.A.DB).FetchOrganisationByID(r.Context(), project.OrganisationID)
	if err != nil {
		log.FromContext(r.Context()).WithError(err).Error("failed to find organisation by id")
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	baseUrl, err := a.retrieveHost()
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	fillSourceURL(source, baseUrl, org.CustomDomain.ValueOrZero())
	_ = render.Render(w, r, util.NewServerResponse("Source fetched successfully", source, http.StatusOK))
}

// UpdateSource
// @Summary Update a source
// @Description This endpoint updates a source
// @Tags Sources
// @Accept  json
// @Produce  json
// @Param projectID path string true "Project ID"
// @Param sourceID path string true "source id"
// @Param source body models.Source true "Source Details"
// @Success 200 {object} util.ServerResponse{data=models.Source}
// @Failure 400,401,404 {object} util.ServerResponse{data=Stub}
// @Security ApiKeyAuth
// @Router /v1/projects/{projectID}/sources/{sourceID} [put]
func (a *PublicHandler) UpdateSource(w http.ResponseWriter, r *http.Request) {
	var sourceUpdate models.UpdateSource
	err := util.ReadJSON(r, &sourceUpdate)
	if err != nil {
		_ = render.Render(w, r, util.NewErrorResponse(err.Error(), http.StatusBadRequest))
		return
	}

	project, err := a.retrieveProject(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	sourceService := createSourceService(a)

	source, err := postgres.NewSourceRepo(a.A.DB).FindSourceByID(r.Context(), project.UID, chi.URLParam(r, "sourceID"))
	if err != nil {
		if err == datastore.ErrSourceNotFound {
			_ = render.Render(w, r, util.NewErrorResponse(err.Error(), http.StatusNotFound))
			return
		}

		_ = render.Render(w, r, util.NewErrorResponse("error retrieving source", http.StatusBadRequest))
		return
	}

	source, err = sourceService.UpdateSource(r.Context(), project, &sourceUpdate, source)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	org, err := postgres.NewOrgRepo(a.A.DB).FetchOrganisationByID(r.Context(), project.OrganisationID)
	if err != nil {
		log.FromContext(r.Context()).WithError(err).Error("failed to find organisation by id")
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	baseUrl, err := a.retrieveHost()
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	fillSourceURL(source, baseUrl, org.CustomDomain.ValueOrZero())
	_ = render.Render(w, r, util.NewServerResponse("Source updated successfully", source, http.StatusAccepted))
}

// DeleteSource
// @Summary Delete a source
// @Description This endpoint deletes a source
// @Tags Sources
// @Accept  json
// @Produce  json
// @Param projectID path string true "Project ID"
// @Param sourceID path string true "source id"
// @Success 200 {object} util.ServerResponse{data=Stub}
// @Failure 400,401,404 {object} util.ServerResponse{data=Stub}
// @Security ApiKeyAuth
// @Router /v1/projects/{projectID}/sources/{sourceID} [delete]
func (a *PublicHandler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	project, err := a.retrieveProject(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	sourceRepo := postgres.NewSourceRepo(a.A.DB)

	source, err := sourceRepo.FindSourceByID(r.Context(), project.UID, chi.URLParam(r, "sourceID"))
	if err != nil {
		if err == datastore.ErrSourceNotFound {
			_ = render.Render(w, r, util.NewErrorResponse(err.Error(), http.StatusNotFound))
			return
		}

		_ = render.Render(w, r, util.NewErrorResponse("error retrieving source", http.StatusBadRequest))
		return
	}

	err = sourceRepo.DeleteSourceByID(r.Context(), project.UID, source.UID, source.VerifierID)
	if err != nil {
		_ = render.Render(w, r, util.NewErrorResponse("failed to delete source", http.StatusBadRequest))
		return
	}

	if source.Provider == datastore.TwitterSourceProvider {
		sourceCacheKey := convoy.SourceCacheKey.Get(source.MaskID).String()
		err = a.A.Cache.Delete(r.Context(), sourceCacheKey)
		if err != nil {
			_ = render.Render(w, r, util.NewErrorResponse("failed to delete source cache", http.StatusBadRequest))
			return
		}
	}

	_ = render.Render(w, r, util.NewServerResponse("Source deleted successfully", nil, http.StatusOK))
}

// LoadSourcesPaged
// @Summary List all sources
// @Description This endpoint fetches multiple sources
// @Tags Sources
// @Accept  json
// @Produce  json
// @Param projectID path string true "Project ID"
// @Param perPage query string false "results per page"
// @Param page query string false "page number"
// @Param sort query string false "sort order"
// @Success 200 {object} util.ServerResponse{data=pagedResponse{content=[]models.Source}}
// @Failure 400,401,404 {object} util.ServerResponse{data=Stub}
// @Security ApiKeyAuth
// @Router /v1/projects/{projectID}/sources [get]
func (a *PublicHandler) LoadSourcesPaged(w http.ResponseWriter, r *http.Request) {
	pageable := m.GetPageableFromContext(r.Context())
	project, err := a.retrieveProject(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	f := &datastore.SourceFilter{
		Type: r.URL.Query().Get("type"),
	}

	sources, paginationData, err := postgres.NewSourceRepo(a.A.DB).LoadSourcesPaged(r.Context(), project.UID, f, pageable)
	if err != nil {
		log.WithError(err).Error("an error occurred while fetching sources")
		_ = render.Render(w, r, util.NewErrorResponse("an error occurred while fetching sources", http.StatusBadRequest))
		return
	}

	baseUrl, err := a.retrieveHost()
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	org, err := a.retrieveOrganisation(r)
	if err != nil {
		_ = render.Render(w, r, util.NewServiceErrResponse(err))
		return
	}

	var customDomain string
	if org == nil {
		customDomain = ""
	} else {
		customDomain = org.CustomDomain.ValueOrZero()
	}

	for i := range sources {
		fillSourceURL(&sources[i], baseUrl, customDomain)
	}

	_ = render.Render(w, r, util.NewServerResponse("Sources fetched successfully", pagedResponse{Content: sources, Pagination: &paginationData}, http.StatusOK))
}

func fillSourceURL(s *datastore.Source, baseUrl string, customDomain string) {
	url := baseUrl
	if len(customDomain) > 0 {
		url = customDomain
	}

	s.URL = fmt.Sprintf("%s/ingest/%s", url, s.MaskID)
}
