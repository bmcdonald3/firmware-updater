// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
//
// This file contains the user-editable OpenAPI extension hook.
//
// ✅ This file is safe to edit: it will NOT be overwritten by regeneration.
//
// Add any routes that are not Fabrica-generated (legacy APIs, custom endpoints,
// WireGuard, cloud-init, etc.) to registerCustomOpenAPIPaths so they appear in
// the served OpenAPI spec and Swagger UI at /openapi.json and /docs.
//
// Example:
//
//	func registerCustomOpenAPIPaths(spec *openapi3.T) {
//	    metaDataOp := openapi3.NewOperation()
//	    metaDataOp.OperationID = "getMetaData"
//	    metaDataOp.Summary = "Cloud-init meta-data endpoint"
//	    metaDataOp.Tags = []string{"cloud-init"}
//	    metaDataOp.Responses = openapi3.NewResponses()
//	    metaDataOp.Responses.Set("200", &openapi3.ResponseRef{
//	        Value: openapi3.NewResponse().WithDescription("YAML metadata for the requesting node"),
//	    })
//	    spec.Paths.Set("/meta-data", &openapi3.PathItem{Get: metaDataOp})
//	}
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"
	"firmware-manager/internal/storage"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

var firmwareProxyDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// registerCustomOpenAPIPaths is called by GenerateOpenAPISpec after all
// Fabrica-generated resource paths have been registered.
// Add your custom / non-generated route definitions here.
func registerCustomOpenAPIPaths(spec *openapi3.T) {
	op := openapi3.NewOperation()
	op.OperationID = "proxyFirmwareLayer"
	op.Summary = "Proxy firmware payload layer"
	op.Description = "Streams a firmware payload layer by digest from a discovered OCI firmware bundle"
	op.Tags = []string{"FirmwareBundle"}

	digestParam := openapi3.NewPathParameter("digest").
		WithDescription("Payload layer digest in sha256 format").
		WithRequired(true).
		WithSchema(openapi3.NewStringSchema())
	op.Parameters = []*openapi3.ParameterRef{{Value: digestParam}}

	op.Responses = openapi3.NewResponses()
	op.Responses.Set("200", &openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("Layer stream")})
	op.Responses.Set("400", &openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("Invalid digest")})
	op.Responses.Set("404", &openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("Layer not found")})
	op.Responses.Set("500", &openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("Proxy failure")})

	spec.Paths.Set("/firmware-proxy/layer/{digest}", &openapi3.PathItem{Get: op})
}

func registerCustomRoutes(r chi.Router) {
	r.Get("/firmware-proxy/layer/{digest}", firmwareProxyLayerHandler)
}

func firmwareProxyLayerHandler(w http.ResponseWriter, r *http.Request) {
	digest := strings.TrimSpace(chi.URLParam(r, "digest"))
	if !firmwareProxyDigestPattern.MatchString(digest) {
		http.Error(w, "invalid digest format", http.StatusBadRequest)
		return
	}

	bundle, layerDesc, err := findBundleAndLayerByDigest(r.Context(), digest)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", strings.TrimSpace(bundle.Spec.RegistryURL), strings.TrimSpace(bundle.Spec.Repository)))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to initialize repository client: %v", err), http.StatusInternalServerError)
		return
	}

	rc, err := repo.Fetch(r.Context(), layerDesc)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch layer: %v", err), http.StatusBadGateway)
		return
	}
	defer rc.Close()

	mediaType := strings.TrimSpace(layerDesc.MediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", layerDesc.Digest.String())
	if layerDesc.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", layerDesc.Size))
	}
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, rc); err != nil {
		http.Error(w, fmt.Sprintf("failed to stream layer: %v", err), http.StatusBadGateway)
	}
}

func findBundleAndLayerByDigest(ctx context.Context, digest string) (*v1.FirmwareBundle, ocispec.Descriptor, error) {
	bundles, err := storage.LoadAllFirmwareBundles(ctx)
	if err != nil {
		return nil, ocispec.Descriptor{}, fmt.Errorf("failed to load firmware bundles: %w", err)
	}

	for _, bundle := range bundles {
		payloadDigest := strings.TrimSpace(bundle.Status.ExtractedMetadata["payloadDigest"])
		if payloadDigest != digest {
			continue
		}
		layers, err := fetchBundleLayers(ctx, bundle)
		if err != nil {
			return nil, ocispec.Descriptor{}, err
		}
		for _, layer := range layers {
			if layer.Digest.String() == digest {
				return bundle, layer, nil
			}
		}
	}

	return nil, ocispec.Descriptor{}, fmt.Errorf("layer digest %s not found in discovered firmware bundles", digest)
}

func fetchBundleLayers(ctx context.Context, bundle *v1.FirmwareBundle) ([]ocispec.Descriptor, error) {
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", strings.TrimSpace(bundle.Spec.RegistryURL), strings.TrimSpace(bundle.Spec.Repository)))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize repository client: %w", err)
	}

	manifestDesc, err := repo.Resolve(ctx, strings.TrimSpace(bundle.Spec.TagOrDigest))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve manifest: %w", err)
	}

	manifestBytes, err := content.FetchAll(ctx, repo, manifestDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	return manifest.Layers, nil
}
