package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/OpenCHAMI/magellan/pkg/secrets"
)

type credentialPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func main() {
	var (
		secretID  string
		username  string
		password  string
		storePath string
	)

	flag.StringVar(&secretID, "secret-id", "", "Secret identifier used by FirmwareUpdateJob spec.secretID")
	flag.StringVar(&username, "username", "", "BMC username to store")
	flag.StringVar(&password, "password", "", "BMC password to store")
	flag.StringVar(&storePath, "store-path", "secrets.json", "Path to encrypted secrets store JSON file")
	flag.Parse()

	if err := validateInputs(secretID, username, password, storePath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	store, err := openStore(storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open store: %v\n", err)
		os.Exit(1)
	}

	payload := credentialPayload{
		Username: strings.TrimSpace(username),
		Password: strings.TrimSpace(password),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal payload: %v\n", err)
		os.Exit(1)
	}

	if err := store.StoreSecretByID(strings.TrimSpace(secretID), string(payloadJSON)); err != nil {
		// magellan v0.5.1 has a SaveSecrets bug that can return
		// "write <file>: file already closed" after updating in-memory data.
		if persistErr := persistStoreFallback(storePath, store); persistErr != nil {
			fmt.Fprintf(os.Stderr, "error: store secret: %v (fallback failed: %v)\n", err, persistErr)
			os.Exit(1)
		}
	}

	fmt.Printf("Stored credentials for secret-id %q in %s\n", strings.TrimSpace(secretID), storePath)
}

func validateInputs(secretID, username, password, storePath string) error {
	secretID = strings.TrimSpace(secretID)
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	storePath = strings.TrimSpace(storePath)

	if secretID == "" {
		return fmt.Errorf("--secret-id is required")
	}
	if username == "" {
		return fmt.Errorf("--username is required")
	}
	if password == "" {
		return fmt.Errorf("--password is required")
	}
	if storePath == "" {
		return fmt.Errorf("--store-path is required")
	}

	masterKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	if len(masterKey) != 64 {
		return fmt.Errorf("MASTER_KEY must be a 64-character hex string")
	}
	decoded, err := hex.DecodeString(masterKey)
	if err != nil {
		return fmt.Errorf("MASTER_KEY must be valid hex: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("MASTER_KEY must decode to 32 bytes for AES-256")
	}

	return nil
}

func openStore(storePath string) (secrets.SecretStore, error) {
	store, err := secrets.OpenStore(storePath)
	if err == nil {
		return store, nil
	}

	if !strings.Contains(strings.ToLower(err.Error()), "file already closed") {
		return nil, err
	}

	masterKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	tmpFile, tmpErr := os.CreateTemp("", "firmware-updater-secret-cli-*.json")
	if tmpErr != nil {
		return nil, fmt.Errorf("create temp fallback store file: %w", tmpErr)
	}
	tmpPath := tmpFile.Name()
	if closeErr := tmpFile.Close(); closeErr != nil {
		return nil, fmt.Errorf("close temp fallback store file: %w", closeErr)
	}
	if removeErr := os.Remove(tmpPath); removeErr != nil {
		return nil, fmt.Errorf("prepare temp fallback store path: %w", removeErr)
	}
	defer os.Remove(tmpPath)

	localStore, localErr := secrets.NewLocalSecretStore(masterKey, tmpPath, true)
	if localErr != nil {
		return nil, fmt.Errorf("create fallback local secret store: %w", localErr)
	}

	if content, readErr := os.ReadFile(storePath); readErr == nil && len(content) > 0 {
		secretMap := make(map[string]string)
		if unmarshalErr := json.Unmarshal(content, &secretMap); unmarshalErr != nil {
			return nil, fmt.Errorf("decode encrypted secrets JSON: %w", unmarshalErr)
		}
		localStore.Secrets = secretMap
	}

	return localStore, nil
}

func persistStoreFallback(storePath string, store secrets.SecretStore) error {
	localStore, ok := store.(*secrets.LocalSecretStore)
	if !ok {
		return fmt.Errorf("unsupported secret store type %T", store)
	}

	encoded, err := json.MarshalIndent(localStore.Secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal encrypted secrets map: %w", err)
	}

	encoded = append(encoded, '\n')
	if err := os.WriteFile(storePath, encoded, 0644); err != nil {
		return fmt.Errorf("write encrypted secrets file: %w", err)
	}

	return nil
}
