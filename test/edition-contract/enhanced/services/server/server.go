// Package server adapts the public Community surface for the workspace fixture.
package server

import community "github.com/semaphoreui/semaphore/community-pro/services/server"

type AwsSmStorageTokenDeserializer = community.AwsSmStorageTokenDeserializer
type AzureKvStorageTokenDeserializer = community.AzureKvStorageTokenDeserializer
type DvlsStorageTokenDeserializer = community.DvlsStorageTokenDeserializer
type VaultStorageTokenDeserializer = community.VaultStorageTokenDeserializer
type AwsSmAccessKeyDeserializer = community.AwsSmAccessKeyDeserializer
type AzureKvAccessKeyDeserializer = community.AzureKvAccessKeyDeserializer
type DvlsAccessKeyDeserializer = community.DvlsAccessKeyDeserializer
type VaultAccessKeyDeserializer = community.VaultAccessKeyDeserializer
type SubscriptionServiceImpl = community.SubscriptionServiceImpl

var (
	GetSecretStorages               = community.GetSecretStorages
	NewAwsSmAccessKeyDeserializer   = community.NewAwsSmAccessKeyDeserializer
	NewAzureKvAccessKeyDeserializer = community.NewAzureKvAccessKeyDeserializer
	NewDvlsAccessKeyDeserializer    = community.NewDvlsAccessKeyDeserializer
	NewSubscriptionService          = community.NewSubscriptionService
	NewVaultAccessKeyDeserializer   = community.NewVaultAccessKeyDeserializer
	NewWorkflowReconciler           = community.NewWorkflowReconciler
	NewWorkflowService              = community.NewWorkflowService
	SyncSecrets                     = community.SyncSecrets
)
