package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/frain-dev/convoy"
	"github.com/frain-dev/convoy/api/models"
	"github.com/frain-dev/convoy/cache"
	"github.com/frain-dev/convoy/datastore"
	log "github.com/frain-dev/convoy/pkg/log"
	"github.com/frain-dev/convoy/queue"
	"github.com/frain-dev/convoy/util"
	"github.com/oklog/ulid/v2"
	"gopkg.in/guregu/null.v4"
)

type EndpointService struct {
	projectRepo       datastore.ProjectRepository
	endpointRepo      datastore.EndpointRepository
	eventRepo         datastore.EventRepository
	eventDeliveryRepo datastore.EventDeliveryRepository
	cache             cache.Cache
	queue             queue.Queuer
}

func NewEndpointService(projectRepo datastore.ProjectRepository, endpointRepo datastore.EndpointRepository, eventRepo datastore.EventRepository, eventDeliveryRepo datastore.EventDeliveryRepository, cache cache.Cache, queue queue.Queuer) *EndpointService {
	return &EndpointService{
		projectRepo:       projectRepo,
		endpointRepo:      endpointRepo,
		eventRepo:         eventRepo,
		eventDeliveryRepo: eventDeliveryRepo,
		cache:             cache,
		queue:             queue,
	}
}

func (a *EndpointService) LoadEndpointsPaged(ctx context.Context, uid string, filter *datastore.Filter, pageable datastore.Pageable) ([]datastore.Endpoint, datastore.PaginationData, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	endpoints, paginationData, err := a.endpointRepo.LoadEndpointsPaged(ctx, uid, filter, pageable)
	if err != nil {
		log.WithError(err).Error("failed to fetch endpoints")
		return nil, datastore.PaginationData{}, util.NewServiceError(http.StatusInternalServerError, errors.New("an error occurred while fetching endpoints"))
	}

	return endpoints, paginationData, nil
}

func (a *EndpointService) CreateEndpoint(ctx context.Context, e models.Endpoint, projectID string) (*datastore.Endpoint, error) {
	if err := util.Validate(e); err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	url, err := util.CleanEndpoint(e.URL)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	e.URL = url
	if e.RateLimit == 0 {
		e.RateLimit = convoy.RATE_LIMIT
	}

	if util.IsStringEmpty(e.RateLimitDuration) {
		e.RateLimitDuration = convoy.RATE_LIMIT_DURATION
	}

	duration, err := time.ParseDuration(e.RateLimitDuration)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("an error occurred parsing the rate limit duration: %v", err))
	}

	project, err := a.projectRepo.FetchProjectByID(ctx, projectID)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("Failed to load endpoint project"))
	}

	if project.Type == datastore.IncomingProject {
		e.AdvancedSignatures = true
	}

	endpoint := &datastore.Endpoint{
		UID:                ulid.Make().String(),
		ProjectID:          projectID,
		OwnerID:            e.OwnerID,
		Title:              e.Name,
		SupportEmail:       e.SupportEmail,
		SlackWebhookURL:    e.SlackWebhookURL,
		TargetURL:          e.URL,
		Description:        e.Description,
		RateLimit:          e.RateLimit,
		HttpTimeout:        e.HttpTimeout,
		AdvancedSignatures: e.AdvancedSignatures,
		AppID:              e.AppID,
		RateLimitDuration:  duration.String(),
		Status:             datastore.ActiveEndpointStatus,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if util.IsStringEmpty(endpoint.AppID) {
		endpoint.AppID = endpoint.UID
	}

	if util.IsStringEmpty(e.Secret) {
		sc, err := util.GenerateSecret()
		if err != nil {
			return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("could not generate secret: %v", err))
		}

		endpoint.Secrets = []datastore.Secret{
			{
				UID:       ulid.Make().String(),
				Value:     sc,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		}
	} else {
		endpoint.Secrets = append(endpoint.Secrets, datastore.Secret{
			UID:       ulid.Make().String(),
			Value:     e.Secret,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	}

	auth, err := ValidateEndpointAuthentication(e.Authentication)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	endpoint.Authentication = auth
	err = a.endpointRepo.CreateEndpoint(ctx, endpoint, projectID)
	if err != nil {
		log.WithError(err).Error("failed to create endpoint")
		return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("an error occurred while adding endpoint"))
	}

	endpointCacheKey := convoy.EndpointsCacheKey.Get(endpoint.UID).String()
	err = a.cache.Set(ctx, endpointCacheKey, &endpoint, time.Minute*5)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, errors.New("failed to update endpoint cache"))
	}

	return endpoint, nil
}

func (a *EndpointService) UpdateEndpoint(ctx context.Context, e models.UpdateEndpoint, endpoint *datastore.Endpoint, project *datastore.Project) (*datastore.Endpoint, error) {
	if err := util.Validate(e); err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	url, err := util.CleanEndpoint(e.URL)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	e.URL = url

	endpoint, err = a.endpointRepo.FindEndpointByID(ctx, endpoint.UID, project.UID)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	endpoint, err = updateEndpoint(endpoint, e, project)
	if err != nil {
		return endpoint, util.NewServiceError(http.StatusBadRequest, err)
	}

	err = a.endpointRepo.UpdateEndpoint(ctx, endpoint, endpoint.ProjectID)
	if err != nil {
		return endpoint, util.NewServiceError(http.StatusBadRequest, errors.New("an error occurred while updating endpoints"))
	}

	endpointCacheKey := convoy.EndpointsCacheKey.Get(endpoint.UID).String()
	err = a.cache.Set(ctx, endpointCacheKey, &endpoint, time.Minute*5)
	if err != nil {
		return endpoint, util.NewServiceError(http.StatusBadRequest, errors.New("failed to update application cache"))
	}

	return endpoint, nil
}

func (a *EndpointService) DeleteEndpoint(ctx context.Context, e *datastore.Endpoint, project *datastore.Project) error {
	err := a.endpointRepo.DeleteEndpoint(ctx, e, project.UID)
	if err != nil {
		log.WithError(err).Error("failed to delete endpoint")
		return util.NewServiceError(http.StatusBadRequest, errors.New("an error occurred while deleting endpoint"))
	}

	endpointCacheKey := convoy.EndpointsCacheKey.Get(e.UID).String()
	err = a.cache.Delete(ctx, endpointCacheKey)
	if err != nil {
		return util.NewServiceError(http.StatusBadRequest, errors.New("failed to delete endpoint cache"))
	}

	return nil
}

func updateEndpoint(endpoint *datastore.Endpoint, e models.UpdateEndpoint, project *datastore.Project) (*datastore.Endpoint, error) {
	endpoint.TargetURL = e.URL
	endpoint.Description = e.Description

	endpoint.Title = *e.Name

	if e.SupportEmail != nil {
		endpoint.SupportEmail = *e.SupportEmail
	}

	if e.SlackWebhookURL != nil {
		endpoint.SlackWebhookURL = *e.SlackWebhookURL
	}

	if e.RateLimit != 0 {
		endpoint.RateLimit = e.RateLimit
	}

	if !util.IsStringEmpty(e.RateLimitDuration) {
		duration, err := time.ParseDuration(e.RateLimitDuration)
		if err != nil {
			return nil, err
		}

		endpoint.RateLimitDuration = duration.String()
	}

	if e.AdvancedSignatures != nil && project.Type == datastore.OutgoingProject {
		endpoint.AdvancedSignatures = *e.AdvancedSignatures
	}

	if !util.IsStringEmpty(e.HttpTimeout) {
		endpoint.HttpTimeout = e.HttpTimeout
	}

	auth, err := ValidateEndpointAuthentication(e.Authentication)
	if err != nil {
		return nil, err
	}

	endpoint.Authentication = auth

	endpoint.UpdatedAt = time.Now()

	return endpoint, nil
}

func (a *EndpointService) ExpireSecret(ctx context.Context, s *models.ExpireSecret, endpoint *datastore.Endpoint, project *datastore.Project) (*datastore.Endpoint, error) {
	// Expire current secret.
	idx, err := endpoint.GetActiveSecretIndex()
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	expiresAt := time.Now().Add(time.Hour * time.Duration(s.Expiration))
	endpoint.Secrets[idx].ExpiresAt = null.TimeFrom(expiresAt)

	secret := endpoint.Secrets[idx]

	// Enqueue for final deletion.
	body := struct {
		EndpointID string `json:"endpoint_id"`
		SecretID   string `json:"secret_id"`
		ProjectID  string `json:"project_id"`
	}{
		EndpointID: endpoint.UID,
		SecretID:   secret.UID,
		ProjectID:  project.UID,
	}

	jobByte, err := json.Marshal(body)
	if err != nil {
		return nil, util.NewServiceError(http.StatusBadRequest, err)
	}

	payload := json.RawMessage(jobByte)

	job := &queue.Job{
		ID:      secret.UID,
		Payload: payload,
		Delay:   time.Hour * time.Duration(s.Expiration),
	}

	taskName := convoy.ExpireSecretsProcessor
	err = a.queue.Write(taskName, convoy.DefaultQueue, job)
	if err != nil {
		log.Errorf("Error occurred sending new event to the queue %s", err)
	}

	// Generate new secret.
	newSecret := s.Secret
	if len(newSecret) == 0 {
		newSecret, err = util.GenerateSecret()
		if err != nil {
			return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf(fmt.Sprintf("could not generate secret...%v", err.Error())))
		}
	}

	sc := datastore.Secret{
		UID:       ulid.Make().String(),
		Value:     newSecret,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	endpoint.Secrets = append(endpoint.Secrets, sc)

	err = a.endpointRepo.UpdateSecrets(ctx, endpoint.UID, project.UID, endpoint.Secrets)
	if err != nil {
		log.Errorf("Error occurred expiring secret %s", err)
		return nil, util.NewServiceError(http.StatusBadRequest, errors.New("failed to expire endpoint secret"))
	}

	endpointCacheKey := convoy.EndpointsCacheKey.Get(endpoint.UID).String()
	err = a.cache.Set(ctx, endpointCacheKey, &endpoint, time.Minute*5)
	if err != nil {
		log.WithError(err).Error("failed to update app cache")
	}

	return endpoint, nil
}

func (s *EndpointService) ToggleEndpointStatus(ctx context.Context, projectID string, endpointId string) (*datastore.Endpoint, error) {
	endpoint, err := s.endpointRepo.FindEndpointByID(ctx, endpointId, projectID)
	if err != nil {
		log.FromContext(ctx).WithError(err).Error(ErrSubscriptionNotFound.Error())
		return nil, util.NewServiceError(http.StatusBadRequest, ErrSubscriptionNotFound)
	}

	switch endpoint.Status {
	case datastore.ActiveEndpointStatus:
		endpoint.Status = datastore.InactiveEndpointStatus
	case datastore.InactiveEndpointStatus:
		endpoint.Status = datastore.ActiveEndpointStatus
	case datastore.PendingEndpointStatus:
		return nil, util.NewServiceError(http.StatusBadRequest, errors.New("endpoint is in pending status"))
	default:
		return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("unknown endpoint status: %s", endpoint.Status))
	}

	err = s.endpointRepo.UpdateEndpointStatus(ctx, projectID, endpoint.UID, endpoint.Status)
	if err != nil {
		log.FromContext(ctx).WithError(err).Error("failed to update endpoint status")
		return nil, util.NewServiceError(http.StatusBadRequest, errors.New("failed to update endpoint status"))
	}

	return endpoint, nil
}

func (s *EndpointService) PauseEndpoint(ctx context.Context, projectID string, endpointId string) (*datastore.Endpoint, error) {
	endpoint, err := s.endpointRepo.FindEndpointByID(ctx, endpointId, projectID)
	if err != nil {
		log.FromContext(ctx).WithError(err).Error(ErrSubscriptionNotFound.Error())
		return nil, util.NewServiceError(http.StatusBadRequest, ErrSubscriptionNotFound)
	}

	switch endpoint.Status {
	case datastore.ActiveEndpointStatus:
		endpoint.Status = datastore.PausedEndpointStatus
	case datastore.PausedEndpointStatus:
		endpoint.Status = datastore.ActiveEndpointStatus
	default:
		return nil, util.NewServiceError(http.StatusBadRequest, fmt.Errorf("current endpoint status - %s, does not support pausing or unpausing", endpoint.Status))
	}

	err = s.endpointRepo.UpdateEndpointStatus(ctx, projectID, endpoint.UID, endpoint.Status)
	if err != nil {
		log.FromContext(ctx).WithError(err).Error("failed to update endpoint status")
		return nil, util.NewServiceError(http.StatusBadRequest, errors.New("failed to update endpoint status"))
	}

	return endpoint, nil
}

func ValidateEndpointAuthentication(auth *datastore.EndpointAuthentication) (*datastore.EndpointAuthentication, error) {
	if auth != nil && !util.IsStringEmpty(string(auth.Type)) {
		if err := util.Validate(auth); err != nil {
			return nil, err
		}

		if auth == nil && auth.Type == datastore.APIKeyAuthentication {
			return nil, util.NewServiceError(http.StatusBadRequest, errors.New("api key field is required"))
		}

		return auth, nil
	}

	return nil, nil
}
