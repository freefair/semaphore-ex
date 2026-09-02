package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const globalCredentialMaterialLimit = 4096

type globalCredentialCipher interface {
	OptionEncryptionEnabled() bool
	EncryptOption([]byte) (string, error)
}

type globalCredentialService struct {
	repository db.GlobalCredentialRepository
	cipher     globalCredentialCipher
	now        func() time.Time
}

func NewGlobalCredentialService(repository db.GlobalCredentialRepository, ciphers ...globalCredentialCipher) pro_interfaces.GlobalCredentialServiceFacade {
	cipher := globalCredentialCipher(util.Config)
	if len(ciphers) > 0 && ciphers[0] != nil {
		cipher = ciphers[0]
	}
	return &globalCredentialService{repository: repository, cipher: cipher, now: func() time.Time { return time.Now().UTC() }}
}

func (s *globalCredentialService) CreateGlobalCredential(ctx context.Context, actorID int, input pro_interfaces.GlobalCredentialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	if input.Type != db.GlobalCredentialTypeString || strings.TrimSpace(input.DisplayName) == "" || len(input.DisplayName) > 128 {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	version, err := s.version(actorID, input.Material)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, err
	}
	record, version, err := s.repository.CreateGlobalCredential(db.GlobalCredential{Type: input.Type, DisplayName: input.DisplayName, OwnerUserID: actorID, Enabled: true, Created: s.now()}, version)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialSummaryDTO(record, version), nil
}

func (s *globalCredentialService) GetGlobalCredential(ctx context.Context, id int) (pro_interfaces.GlobalCredentialDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || id <= 0 {
		return pro_interfaces.GlobalCredentialDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	record, err := s.repository.GetGlobalCredential(id)
	if err != nil {
		return pro_interfaces.GlobalCredentialDTO{}, mapGlobalCredentialError(err)
	}
	version, err := s.repository.GetGlobalCredentialVersion(id, record.CurrentVersion)
	if err != nil {
		return pro_interfaces.GlobalCredentialDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialDTO(record, version), nil
}

func (s *globalCredentialService) ListGlobalCredentials(ctx context.Context, params db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialSummaryDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || validateGlobalCredentialPagination(params) != nil {
		return nil, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	records, err := s.repository.GetGlobalCredentials(params)
	if err != nil {
		return nil, mapGlobalCredentialError(err)
	}
	result := make([]pro_interfaces.GlobalCredentialSummaryDTO, 0, len(records))
	for _, record := range records {
		version, versionErr := s.repository.GetGlobalCredentialVersion(record.ID, record.CurrentVersion)
		if versionErr != nil {
			return nil, mapGlobalCredentialError(versionErr)
		}
		result = append(result, globalCredentialSummaryDTO(record, version))
	}
	return result, nil
}

func (s *globalCredentialService) UpdateGlobalCredential(ctx context.Context, actorID, id, revision int, input pro_interfaces.GlobalCredentialMetadataInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || id <= 0 || revision <= 0 ||
		strings.TrimSpace(input.DisplayName) == "" || len(input.DisplayName) > 128 {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	current, err := s.repository.GetGlobalCredential(id)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	updated, err := s.repository.UpdateGlobalCredentialMetadata(db.GlobalCredential{ID: id, Type: current.Type, DisplayName: input.DisplayName, OwnerUserID: current.OwnerUserID, Revision: revision, CurrentVersion: current.CurrentVersion}, revision, s.now())
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	version, err := s.repository.GetGlobalCredentialVersion(id, updated.CurrentVersion)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialSummaryDTO(updated, version), nil
}

func (s *globalCredentialService) SetGlobalCredentialEnabled(ctx context.Context, actorID, id, revision int, enabled bool) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || id <= 0 || revision <= 0 {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	updated, err := s.repository.SetGlobalCredentialEnabled(id, enabled, revision, s.now())
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	version, err := s.repository.GetGlobalCredentialVersion(id, updated.CurrentVersion)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialSummaryDTO(updated, version), nil
}

func (s *globalCredentialService) RotateGlobalCredential(ctx context.Context, actorID, id, revision int, input pro_interfaces.GlobalCredentialMaterialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || id <= 0 || revision <= 0 {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	version, err := s.version(actorID, &input)
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, err
	}
	updated, version, err := s.repository.RotateGlobalCredential(id, revision, version, s.now())
	if err != nil {
		return pro_interfaces.GlobalCredentialSummaryDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialSummaryDTO(updated, version), nil
}

func (s *globalCredentialService) DeleteGlobalCredential(ctx context.Context, actorID, id, revision int) error {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || id <= 0 || revision <= 0 {
		return pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	return mapGlobalCredentialError(s.repository.DeleteGlobalCredential(id, revision))
}

func (s *globalCredentialService) CreateGlobalCredentialGrant(ctx context.Context, actorID, credentialID int, input pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || credentialID <= 0 {
		return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	grant := db.GlobalCredentialGrant{CredentialID: credentialID, ProjectID: input.ProjectID, Operations: input.Operations, ExpiresAt: input.ExpiresAt, Status: db.GlobalCredentialGrantStatusActive, Revision: 1, CreatedByUserID: actorID, Created: s.now()}
	if err := db.ValidateGlobalCredentialGrant(grant); err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	grant, err := s.repository.CreateGlobalCredentialGrant(grant)
	if err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialGrantDTO(grant), nil
}

func (s *globalCredentialService) ListGlobalCredentialGrants(ctx context.Context, credentialID int, params db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialGrantDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || credentialID <= 0 || validateGlobalCredentialPagination(params) != nil {
		return nil, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	grants, err := s.repository.GetGlobalCredentialGrants(credentialID, params)
	if err != nil {
		return nil, mapGlobalCredentialError(err)
	}
	result := make([]pro_interfaces.GlobalCredentialGrantDTO, len(grants))
	for index, grant := range grants {
		result[index] = globalCredentialGrantDTO(grant)
	}
	return result, nil
}

func (s *globalCredentialService) UpdateGlobalCredentialGrant(ctx context.Context, actorID, credentialID, grantID, revision int, input pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || credentialID <= 0 || grantID <= 0 || revision <= 0 {
		return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	current, err := s.repository.GetGlobalCredentialGrant(credentialID, grantID)
	if err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, mapGlobalCredentialError(err)
	}
	grant := db.GlobalCredentialGrant{ID: grantID, CredentialID: credentialID, ProjectID: input.ProjectID, Operations: input.Operations, ExpiresAt: input.ExpiresAt, Status: current.Status, Revision: revision, CreatedByUserID: current.CreatedByUserID, RevokedByUserID: current.RevokedByUserID, RevokedAt: current.RevokedAt}
	if err := db.ValidateGlobalCredentialGrant(grant); err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	grant, err = s.repository.UpdateGlobalCredentialGrant(grant, revision, s.now())
	if err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialGrantDTO(grant), nil
}

func (s *globalCredentialService) SetGlobalCredentialGrantStatus(ctx context.Context, actorID, credentialID, grantID, revision int, status db.GlobalCredentialGrantStatus) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || credentialID <= 0 || grantID <= 0 || revision <= 0 {
		return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	grant, err := s.repository.SetGlobalCredentialGrantStatus(credentialID, grantID, status, revision, &actorID, s.now())
	if err != nil {
		return pro_interfaces.GlobalCredentialGrantDTO{}, mapGlobalCredentialError(err)
	}
	return globalCredentialGrantDTO(grant), nil
}

func (s *globalCredentialService) DeleteGlobalCredentialGrant(ctx context.Context, actorID, credentialID, grantID, revision int) error {
	if err := globalCredentialContext(ctx); err != nil || actorID <= 0 || credentialID <= 0 || grantID <= 0 || revision <= 0 {
		return pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	return mapGlobalCredentialError(s.repository.DeleteGlobalCredentialGrant(credentialID, grantID, revision))
}

func (s *globalCredentialService) ListGlobalCredentialGrantProjects(ctx context.Context) ([]pro_interfaces.GlobalCredentialGrantProjectDTO, error) {
	if err := globalCredentialContext(ctx); err != nil {
		return nil, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	projects, err := s.repository.GetGlobalCredentialGrantProjects()
	if err != nil {
		return nil, mapGlobalCredentialError(err)
	}
	result := make([]pro_interfaces.GlobalCredentialGrantProjectDTO, len(projects))
	for index, project := range projects {
		result[index] = pro_interfaces.GlobalCredentialGrantProjectDTO{ID: project.ID, Name: project.Name}
	}
	return result, nil
}

func (s *globalCredentialService) ListGrantedCredentials(ctx context.Context, projectID int, params db.RetrieveQueryParams) ([]pro_interfaces.GrantedCredentialDTO, error) {
	if err := globalCredentialContext(ctx); err != nil || projectID <= 0 || validateGlobalCredentialPagination(params) != nil {
		return nil, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	metadata, err := s.repository.GetEffectiveGlobalCredentialMetadata(projectID, s.now(), params)
	if err != nil {
		return nil, mapGlobalCredentialError(err)
	}
	result := make([]pro_interfaces.GrantedCredentialDTO, len(metadata))
	for index, value := range metadata {
		result[index] = pro_interfaces.GrantedCredentialDTO{CredentialID: value.CredentialID, Type: value.Type, DisplayName: value.DisplayName, Version: value.Version, Operations: value.Operations, GrantID: value.GrantID, GrantRevision: value.GrantRevision, ExpiresAt: value.ExpiresAt}
	}
	return result, nil
}

func (s *globalCredentialService) version(actorID int, input *pro_interfaces.GlobalCredentialMaterialInput) (db.GlobalCredentialVersion, error) {
	if input == nil || (input.StringValue == nil) == (input.ExternalReference == nil) {
		return db.GlobalCredentialVersion{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	if input.ExternalReference != nil {
		if err := input.ExternalReference.Validate(); err != nil {
			return db.GlobalCredentialVersion{}, pro_interfaces.ErrGlobalCredentialInvalidInput
		}
		return db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialExternalReference, ExternalReference: *input.ExternalReference, CreatedByUserID: actorID}, nil
	}
	if !s.cipher.OptionEncryptionEnabled() {
		return db.GlobalCredentialVersion{}, pro_interfaces.ErrGlobalCredentialEncryptionRequired
	}
	value := []byte(*input.StringValue)
	defer func() {
		for index := range value {
			value[index] = 0
		}
	}()
	if len(value) == 0 || len(value) > globalCredentialMaterialLimit {
		return db.GlobalCredentialVersion{}, pro_interfaces.ErrGlobalCredentialInvalidInput
	}
	sealed, err := s.cipher.EncryptOption(value)
	if err != nil {
		return db.GlobalCredentialVersion{}, pro_interfaces.ErrGlobalCredentialNotAvailable
	}
	return db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: sealed, CreatedByUserID: actorID}, nil
}

func globalCredentialDTO(record db.GlobalCredential, version db.GlobalCredentialVersion) pro_interfaces.GlobalCredentialDTO {
	dto := pro_interfaces.GlobalCredentialDTO{ID: record.ID, Type: record.Type, DisplayName: record.DisplayName, OwnerUserID: record.OwnerUserID, Enabled: record.Enabled, Revision: record.Revision, CurrentVersion: record.CurrentVersion, Fingerprint: version.Fingerprint, MaterialKind: version.MaterialKind, Created: record.Created, Updated: record.Updated}
	if version.MaterialKind == db.GlobalCredentialMaterialExternalReference {
		reference := version.ExternalReference
		dto.ExternalReference = &reference
	}
	return dto
}

func globalCredentialSummaryDTO(record db.GlobalCredential, version db.GlobalCredentialVersion) pro_interfaces.GlobalCredentialSummaryDTO {
	return pro_interfaces.GlobalCredentialSummaryDTO{ID: record.ID, Type: record.Type, DisplayName: record.DisplayName, Enabled: record.Enabled, Revision: record.Revision, CurrentVersion: record.CurrentVersion, Fingerprint: version.Fingerprint, MaterialKind: version.MaterialKind}
}
func globalCredentialGrantDTO(value db.GlobalCredentialGrant) pro_interfaces.GlobalCredentialGrantDTO {
	return pro_interfaces.GlobalCredentialGrantDTO{ID: value.ID, CredentialID: value.CredentialID, ProjectID: value.ProjectID, Operations: value.Operations, ExpiresAt: value.ExpiresAt, Status: value.Status, Revision: value.Revision, Created: value.Created, Updated: value.Updated}
}
func validateGlobalCredentialPagination(params db.RetrieveQueryParams) error {
	if params.Offset < 0 || params.Count < 1 || params.Count > 100 {
		return errors.New("invalid pagination")
	}
	return nil
}
func globalCredentialContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
func mapGlobalCredentialError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.ErrGlobalCredentialNotFound
	}
	if errors.Is(err, db.ErrGlobalCredentialGrantConflict) {
		return pro_interfaces.ErrGlobalCredentialGrantConflict
	}
	if errors.Is(err, db.ErrGlobalCredentialRevisionConflict) {
		return pro_interfaces.ErrGlobalCredentialRevisionConflict
	}
	if errors.Is(err, db.ErrGlobalCredentialGrantExists) {
		return pro_interfaces.ErrGlobalCredentialGrantExists
	}
	if errors.Is(err, db.ErrGlobalCredentialEnabled) || errors.Is(err, db.ErrGlobalCredentialGrantsExist) || errors.Is(err, db.ErrGlobalCredentialGrantDependency) {
		return pro_interfaces.ErrGlobalCredentialDependencyConflict
	}
	return err
}

var _ pro_interfaces.GlobalCredentialServiceFacade = (*globalCredentialService)(nil)
