// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package packslip

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type dbcReleaseExtension struct {
	SchemaVersion int    `json:"schema_version"`
	DriverID      string `json:"driver_id"`
}

type dbcArtifactExtension struct {
	PackageVersion int `json:"package_version"`
}

// validateDBCReleaseExtensions applies dbc's consumer contract to a verified
// Packslip release. Other Packslip extensions remain owned by their consumers.
func validateDBCReleaseExtensions(release *parsedRelease) error {
	if release == nil {
		return errors.New("packslip release is nil")
	}
	raw, ok := release.predicate.Extensions["dbc"]
	if !ok {
		return errors.New("packslip release is missing predicate.extensions.dbc")
	}
	var declaration dbcReleaseExtension
	if err := decodeDBCObject(raw, &declaration); err != nil {
		return fmt.Errorf("invalid packslip predicate.extensions.dbc declaration: %w", err)
	}
	if declaration.SchemaVersion != 1 {
		return fmt.Errorf("unsupported dbc release extension schema_version %d", declaration.SchemaVersion)
	}
	if err := validateRuntimeDriverID(declaration.DriverID); err != nil {
		return fmt.Errorf("invalid dbc release driver_id %q: %w", declaration.DriverID, err)
	}
	release.driverID = declaration.DriverID

	for i := range release.predicate.Artifacts {
		artifact := &release.predicate.Artifacts[i]
		raw, ok := artifact.Extensions["dbc"]
		if !ok {
			continue
		}
		var declaration dbcArtifactExtension
		if err := decodeDBCObject(raw, &declaration); err != nil {
			return fmt.Errorf("invalid packslip artifact %q extensions.dbc declaration: %w", artifact.Name, err)
		}
		if declaration.PackageVersion != 2 {
			return fmt.Errorf("unsupported dbc package_version %d on packslip artifact %q", declaration.PackageVersion, artifact.Name)
		}
		artifact.dbcPackageVersion = declaration.PackageVersion
	}
	return nil
}

func decodeDBCObject(raw json.RawMessage, destination any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("must be a non-null object")
	}
	if err := decodeStrict(trimmed, destination); err != nil {
		return err
	}
	return nil
}

func validateRuntimeDriverID(id string) error {
	if id == "" || id == "." || id == ".." {
		return errors.New("ID must be a non-empty flat file name")
	}
	if strings.ContainsAny(id, "/\\\x00:") {
		return errors.New("absolute paths and path separators are not allowed")
	}
	if strings.TrimSpace(id) != id || strings.HasSuffix(id, ".") {
		return errors.New("trailing spaces and dots are not allowed")
	}
	for _, char := range id {
		if char < 0x20 || strings.ContainsRune(`<>"|?*`, char) {
			return errors.New("ID contains a character unsupported by Windows")
		}
	}
	deviceName := strings.ToUpper(strings.TrimRight(strings.SplitN(id, ".", 2)[0], " ."))
	switch deviceName {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return errors.New("ID is reserved by Windows")
	}
	for _, prefix := range []string{"COM", "LPT"} {
		for _, suffix := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
			if strings.EqualFold(deviceName, prefix+suffix) {
				return errors.New("ID is reserved by Windows")
			}
		}
	}
	return nil
}
