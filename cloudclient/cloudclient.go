// Package cloudclient re-exports repoguide-cli's internal cloud backend
// client so other modules (e.g. repoguide-cloud/backend's compliance tests)
// can drive the real client against a test server. Go's internal package
// visibility rule blocks direct cross-module imports of internal/sessionimport,
// hence this thin public alias.
package cloudclient

import "github.com/repoguide/repoguide-cli/internal/sessionimport"

type CloudClient = sessionimport.CloudClient

var NewCloudClient = sessionimport.NewCloudClient
