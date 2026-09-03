package db

import (
	"strings"
	"testing"
	"time"
)

func validWorkflowFileArtifactUpload() WorkflowFileArtifactUpload {
	return WorkflowFileArtifactUpload{
		WorkflowNodeID: 3, TaskID: 41, Attempt: 2,
		LogicalName: "release-bundle", Filename: "release.tar.gz",
		MediaType: "application/gzip", SizeBytes: 1024, SHA256: strings.Repeat("a", 64),
		AccessPolicy: WorkflowFileArtifactAccessPolicy{
			Revision: 1, RoleIDs: []ProjectRoleReference{BuiltinProjectRoleReferenceOwner},
		},
	}
}

func TestWorkflowFileArtifactUploadValidation(t *testing.T) {
	if err := validWorkflowFileArtifactUpload().Validate(); err != nil {
		t.Fatalf("valid upload rejected: %v", err)
	}
	initialAttempt := validWorkflowFileArtifactUpload()
	initialAttempt.Attempt = 0
	if err := initialAttempt.Validate(); err != nil {
		t.Fatalf("initial local task attempt rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*WorkflowFileArtifactUpload)
	}{
		{"path traversal", func(value *WorkflowFileArtifactUpload) { value.Filename = "../secret" }},
		{"header injection", func(value *WorkflowFileArtifactUpload) { value.Filename = "safe.txt\r\nX-Test: yes" }},
		{"invalid media type", func(value *WorkflowFileArtifactUpload) { value.MediaType = "text/plain\r\nX-Test: yes" }},
		{"oversize", func(value *WorkflowFileArtifactUpload) { value.SizeBytes = MaxWorkflowFileArtifactBytes + 1 }},
		{"invalid checksum", func(value *WorkflowFileArtifactUpload) { value.SHA256 = strings.Repeat("A", 64) }},
		{"negative runtime node ID", func(value *WorkflowFileArtifactUpload) { value.WorkflowNodeID = -1 }},
		{"negative attempt", func(value *WorkflowFileArtifactUpload) { value.Attempt = -1 }},
		{"invalid role", func(value *WorkflowFileArtifactUpload) {
			value.AccessPolicy.RoleIDs = []ProjectRoleReference{"bad role"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validWorkflowFileArtifactUpload()
			test.mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid upload accepted")
			}
		})
	}
}

func TestWorkflowFileArtifactStoragePrimitivesAreBounded(t *testing.T) {
	chunk := WorkflowFileArtifactChunk{
		ArtifactID: 1, Ordinal: 0, OffsetBytes: 0,
		SizeBytes: 3, Data: []byte("abc"),
	}
	if err := chunk.Validate(); err != nil {
		t.Fatalf("valid chunk rejected: %v", err)
	}
	chunk.SizeBytes++
	if err := chunk.Validate(); err == nil {
		t.Fatal("chunk with mismatched declared size accepted")
	}

	created := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	lease := WorkflowFileArtifactDownloadLease{
		LeaseToken: strings.Repeat("a", 64), ArtifactID: 1,
		CreatedAt: created, ExpiresAt: created.Add(MaxWorkflowFileArtifactDownloadLease),
	}
	if err := lease.Validate(); err != nil {
		t.Fatalf("valid download lease rejected: %v", err)
	}
	lease.ExpiresAt = created.Add(MaxWorkflowFileArtifactDownloadLease + time.Second)
	if err := lease.Validate(); err == nil {
		t.Fatal("download lease beyond the hard maximum accepted")
	}

	usage := WorkflowFileArtifactRunUsage{
		WorkflowRunID: 5, ReservedBytes: MaxWorkflowFileArtifactRunBytes,
		ArtifactCount: MaxWorkflowFileArtifactsPerRun, Revision: 1,
	}
	if err := usage.Validate(); err != nil {
		t.Fatalf("valid run usage rejected: %v", err)
	}
	usage.ReservedBytes++
	if err := usage.Validate(); err == nil {
		t.Fatal("run usage beyond the hard maximum accepted")
	}
}

func TestDefaultWorkflowArtifactRetentionSnapshotUsesBuiltInRevision(t *testing.T) {
	snapshot := DefaultWorkflowArtifactRetentionSnapshot()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("built-in retention snapshot rejected: %v", err)
	}
	if snapshot.GlobalRevision != 0 || snapshot.RetentionSeconds != DefaultWorkflowArtifactRetentionSeconds ||
		snapshot.MaxArtifactBytes != MaxWorkflowFileArtifactBytes || snapshot.MaxRunBytes != MaxWorkflowFileArtifactRunBytes {
		t.Fatalf("unexpected built-in retention snapshot: %+v", snapshot)
	}
}

func TestResolveWorkflowArtifactRetentionRequiresProjectNarrowing(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	global := WorkflowArtifactRetentionPolicy{
		Scope: WorkflowArtifactRetentionGlobal, Revision: 4,
		RetentionSeconds: int64((30 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 32 << 20, MaxRunBytes: 128 << 20,
		CreatedByUserID: 7, CreatedAt: now,
	}
	projectID := 9
	project := WorkflowArtifactRetentionPolicy{
		Scope: WorkflowArtifactRetentionProject, ProjectID: &projectID, Revision: 2,
		RetentionSeconds: int64((7 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 16 << 20, MaxRunBytes: 64 << 20,
		CreatedByUserID: 8, CreatedAt: now,
	}
	snapshot, err := ResolveWorkflowArtifactRetention(global, &project)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GlobalRevision != 4 || snapshot.ProjectRevision != 2 ||
		snapshot.RetentionSeconds != project.RetentionSeconds ||
		snapshot.MaxArtifactBytes != project.MaxArtifactBytes || snapshot.MaxRunBytes != project.MaxRunBytes {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	project.MaxArtifactBytes = global.MaxArtifactBytes + 1
	if _, err = ResolveWorkflowArtifactRetention(global, &project); err == nil {
		t.Fatal("project policy widened the global artifact limit")
	}
}

func TestAvailableWorkflowFileArtifactRequiresCompleteProvenance(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	finalized := now.Add(time.Minute)
	expires := finalized.Add(7 * 24 * time.Hour)
	metadata := WorkflowFileArtifactMetadata{
		ID: 1, ProjectID: 2, WorkflowTemplateID: 4, WorkflowRunID: 5,
		WorkflowNodeID: 3, WorkflowDefinitionRevision: 6, TaskID: 41, Attempt: 2,
		LogicalName: "release-bundle", Filename: "release.tar.gz", MediaType: "application/gzip",
		SizeBytes: 1024, UploadedBytes: 1024, SHA256: strings.Repeat("a", 64),
		State: WorkflowFileArtifactAvailable, Revision: 3,
		ProducerUserID: 7, ProducerTemplateID: 11, ProducerVersion: "sha256:producer",
		CredentialProvenance: []WorkflowFileArtifactCredentialProvenance{{
			CredentialID: 13, GrantID: 17, CredentialVersion: 3,
			VersionFingerprint: strings.Repeat("b", 64), Target: "environment.deploy_token",
		}},
		AccessPolicy: WorkflowFileArtifactAccessPolicy{
			Revision: 1, RoleIDs: []ProjectRoleReference{BuiltinProjectRoleReferenceOwner},
		},
		Retention: WorkflowArtifactRetentionSnapshot{
			GlobalRevision: 4, ProjectRevision: 2,
			RetentionSeconds: int64((7 * 24 * time.Hour) / time.Second),
			MaxArtifactBytes: 16 << 20, MaxRunBytes: 64 << 20,
		},
		CreatedAt: now, FinalizedAt: &finalized, ExpiresAt: &expires,
	}
	if err := metadata.CanonicalizeForPersistence(); err != nil {
		t.Fatalf("valid metadata cannot be prepared for persistence: %v", err)
	}
	if err := metadata.Validate(); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}

	negativeNodeID := metadata
	negativeNodeID.WorkflowNodeID = -1
	if negativeNodeID.Validate() == nil {
		t.Fatal("available artifact accepted a negative persisted workflow node ID")
	}

	for _, expiresAt := range []time.Time{
		finalized.Add(time.Second),
		finalized.Add(8 * 24 * time.Hour),
	} {
		candidate := metadata
		candidate.ExpiresAt = &expiresAt
		if candidate.Validate() == nil {
			t.Fatal("available artifact accepted an expiry outside retention")
		}
	}

	persistenceMismatches := []struct {
		name   string
		mutate func(*WorkflowFileArtifactMetadata)
	}{
		{"credential provenance JSON", func(value *WorkflowFileArtifactMetadata) {
			value.CredentialProvenanceJSON = "[]"
		}},
		{"access policy JSON", func(value *WorkflowFileArtifactMetadata) {
			value.AccessPolicyJSON = `{"revision":99}`
		}},
		{"global retention revision", func(value *WorkflowFileArtifactMetadata) {
			value.RetentionGlobalRevision++
		}},
		{"project retention revision", func(value *WorkflowFileArtifactMetadata) {
			value.RetentionProjectRevision++
		}},
		{"retention duration", func(value *WorkflowFileArtifactMetadata) {
			value.RetentionSeconds++
		}},
		{"maximum artifact size", func(value *WorkflowFileArtifactMetadata) {
			value.RetentionMaxArtifactBytes++
		}},
		{"maximum run size", func(value *WorkflowFileArtifactMetadata) {
			value.RetentionMaxRunBytes++
		}},
	}
	for _, test := range persistenceMismatches {
		t.Run(test.name, func(t *testing.T) {
			candidate := metadata
			test.mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("available artifact accepted mismatched persistence")
			}
		})
	}

	metadata.UploadedBytes--
	if metadata.Validate() == nil {
		t.Fatal("available artifact accepted incomplete content")
	}
}
