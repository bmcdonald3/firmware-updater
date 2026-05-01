// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

// RegisterCustomRoutes registers custom routes including the firmware file server
func RegisterCustomRoutes(r chi.Router) {
	// Ensure firmware_payloads directory exists
	if err := os.MkdirAll("./firmware_payloads", 0755); err != nil {
		// Log but don't fail - directory might already exist
	}

	// Mount the file server at /firmware-files/
	// This serves files from ./firmware_payloads directory
	fileServer := http.FileServer(http.Dir("./firmware_payloads"))
	r.Handle("/firmware-files/*", http.StripPrefix("/firmware-files", fileServer))
}
