package db

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CrossProjectTemplateReference is a workflow-definition reference to one
// immutable owner template version. API callers may supply only GrantID and
// TemplateVersionNumber; every other field is resolved from the active grant
// and published template version by the Enhanced service.
type CrossProjectTemplateReference struct {
	GrantID               int    `json:"grant_id"`
	TemplateVersionNumber int    `json:"template_version_number"`
	OwnerProjectID        int    `json:"owner_project_id,omitempty"`
	TemplateID            int    `json:"template_id,omitempty"`
	TemplateVersionID     int    `json:"template_version_id,omitempty"`
	ContentFingerprint    string `json:"content_fingerprint,omitempty"`
	GrantRevision         int    `json:"grant_revision,omitempty"`
}

func (reference CrossProjectTemplateReference) ValidateInput() error {
	if reference.GrantID <= 0 || reference.TemplateVersionNumber <= 0 {
		return errors.New("cross-project template reference requires a grant and exact version")
	}
	return nil
}

func (reference CrossProjectTemplateReference) ValidateNormalized() error {
	if err := reference.ValidateInput(); err != nil {
		return err
	}
	if reference.OwnerProjectID <= 0 || reference.TemplateID <= 0 || reference.TemplateVersionID <= 0 ||
		reference.GrantRevision <= 0 || !isTemplateVersionFingerprint(reference.ContentFingerprint) {
		return errors.New("cross-project template reference provenance is invalid")
	}
	return nil
}

// CrossProjectTemplateProvenance freezes every non-secret input needed to
// identify a cross-project template execution. Values remain owner-side and
// are resolved only by the later dispatch implementation.
type CrossProjectTemplateProvenance struct {
	Reference        CrossProjectTemplateReference `json:"reference"`
	TemplateSnapshot TemplateVersionSnapshot       `json:"template_snapshot"`
}

func (provenance CrossProjectTemplateProvenance) Validate() error {
	if err := provenance.Reference.ValidateNormalized(); err != nil {
		return err
	}
	if err := provenance.TemplateSnapshot.Validate(); err != nil {
		return err
	}
	fingerprint, err := TemplateVersionFingerprint(provenance.TemplateSnapshot)
	if err != nil {
		return err
	}
	if fingerprint != provenance.Reference.ContentFingerprint {
		return errors.New("cross-project template provenance fingerprint does not match snapshot")
	}
	return nil
}

func (provenance CrossProjectTemplateProvenance) CanonicalJSON() (string, error) {
	if err := provenance.Validate(); err != nil {
		return "", err
	}
	canonicalSnapshot, err := provenance.TemplateSnapshot.CanonicalJSON()
	if err != nil {
		return "", err
	}
	var snapshot TemplateVersionSnapshot
	if err = json.Unmarshal(canonicalSnapshot, &snapshot); err != nil {
		return "", fmt.Errorf("decode canonical template version snapshot: %w", err)
	}
	provenance.TemplateSnapshot = snapshot
	payload, err := json.Marshal(provenance)
	if err != nil {
		return "", fmt.Errorf("encode cross-project template provenance: %w", err)
	}
	return string(payload), nil
}

func DecodeCrossProjectTemplateReference(payload string) (*CrossProjectTemplateReference, error) {
	if payload == "" {
		return nil, nil
	}
	var reference CrossProjectTemplateReference
	if err := json.Unmarshal([]byte(payload), &reference); err != nil {
		return nil, fmt.Errorf("decode cross-project template reference: %w", err)
	}
	if err := reference.ValidateNormalized(); err != nil {
		return nil, err
	}
	return &reference, nil
}

func DecodeCrossProjectTemplateProvenance(payload string) (*CrossProjectTemplateProvenance, error) {
	if payload == "" {
		return nil, nil
	}
	var provenance CrossProjectTemplateProvenance
	if err := json.Unmarshal([]byte(payload), &provenance); err != nil {
		return nil, fmt.Errorf("decode cross-project template provenance: %w", err)
	}
	if err := provenance.Validate(); err != nil {
		return nil, err
	}
	return &provenance, nil
}

// WorkflowTemplateProvenance is the durable, value-free grant-aware task
// provenance used by the fenced cross-project dispatcher.
type WorkflowTemplateProvenance struct {
	CrossProject *CrossProjectTemplateProvenance `json:"cross_project,omitempty"`
}

func (provenance WorkflowTemplateProvenance) Validate() error {
	if provenance.CrossProject == nil {
		return errors.New("workflow template provenance is empty")
	}
	return provenance.CrossProject.Validate()
}

// CanonicalJSON produces durable task provenance without including any
// resolved repository, environment, or credential values.
func (provenance WorkflowTemplateProvenance) CanonicalJSON() (string, error) {
	if err := provenance.Validate(); err != nil {
		return "", err
	}
	canonicalCrossProject, err := provenance.CrossProject.CanonicalJSON()
	if err != nil {
		return "", err
	}
	var canonical CrossProjectTemplateProvenance
	if err = json.Unmarshal([]byte(canonicalCrossProject), &canonical); err != nil {
		return "", fmt.Errorf("decode canonical cross-project task provenance: %w", err)
	}
	provenance.CrossProject = &canonical
	payload, err := json.Marshal(provenance)
	if err != nil {
		return "", fmt.Errorf("encode workflow template provenance: %w", err)
	}
	return string(payload), nil
}

func DecodeWorkflowTemplateProvenance(payload string) (*WorkflowTemplateProvenance, error) {
	if payload == "" {
		return nil, nil
	}
	var provenance WorkflowTemplateProvenance
	if err := json.Unmarshal([]byte(payload), &provenance); err != nil {
		return nil, fmt.Errorf("decode workflow template provenance: %w", err)
	}
	if err := provenance.Validate(); err != nil {
		return nil, err
	}
	return &provenance, nil
}
