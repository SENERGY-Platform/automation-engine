/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package transfer moves the deployments of a gateway into a new cloud (SNRGY-5066): the
// subcommand export-gateway reads them on the gateway, PUT /networks/{id}/import takes them in
// the cloud.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// DefaultSqlitePath is the database of the process module on a gateway.
const DefaultSqlitePath = "/root/data/automation-engine.db"

// Main runs the subcommand export-gateway: it writes the export of the database as JSON to
// stdout and returns the exit code, 0 on success. The database is only read, never migrated,
// so that it runs in a throwaway container beside the module with the volume mounted
// read-only.
func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("export-gateway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("sqlite", "", "database of the engine; default SQLITE_PATH, else "+DefaultSqlitePath)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "unexpected arguments:", strings.Join(flags.Args(), " "))
		return 2
	}
	if *path == "" {
		*path = os.Getenv("SQLITE_PATH")
	}
	if *path == "" {
		*path = DefaultSqlitePath
	}
	export, err := Export(context.Background(), *path, exporterVersion(), time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "export not possible:", err)
		return 1
	}
	if err = json.NewEncoder(stdout).Encode(export); err != nil {
		_, _ = fmt.Fprintln(stderr, "export not written:", err)
		return 1
	}
	return 0
}

// Export reads the deployments of the gateway database at path in one transaction.
func Export(ctx context.Context, path string, version string, now time.Time) (result model.GatewayExport, err error) {
	_, walErr := os.Stat(path + "-wal")
	s, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return result, err
	}
	defer s.Close()
	err = s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.GatewayExport()
		return err
	})
	// without a -wal file the database was read as one that cannot change; an engine that
	// started meanwhile may have changed it under the export
	if _, statErr := os.Stat(path + "-wal"); walErr != nil && statErr == nil {
		return result, errors.New("the engine started during the export; repeat it")
	}
	if err != nil {
		return result, err
	}
	result.Format, result.FormatVersion, result.ExportedAt, result.ExporterVersion = model.GatewayExportFormat, model.GatewayExportVersion, now, version
	return result, nil
}

// exporterVersion is the release tag the image was built with (version.txt in the working
// directory, see Dockerfile), else the version of the build
func exporterVersion() string {
	if content, err := os.ReadFile("version.txt"); err == nil {
		if fields := strings.Fields(string(content)); len(fields) > 0 && fields[0] != "dev" {
			return fields[0]
		}
	}
	return server.Version()
}
