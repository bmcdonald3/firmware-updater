// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains custom route extensions and middleware

package main

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

// RegisterCustomRoutes registers custom HTTP routes including the firmware file server
func RegisterCustomRoutes(r *chi.Mux) error {
	// Create firmware_payloads directory if it doesn't exist
	firmwareDir := "firmware_payloads"
	if err := os.MkdirAll(firmwareDir, 0755); err != nil {
		log.Printf("Warning: failed to create firmware_payloads directory: %v", err)
		return err
	}

	// Register the HTTP file server for firmware files
	fileServer := http.FileServer(http.Dir(firmwareDir))
	r.Mount("/firmware-files", http.StripPrefix("/firmware-files", fileServer))
	log.Println("Registered firmware file server at /firmware-files")

	return nil
}
