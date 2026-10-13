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

package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

// OldServices are the sources of a migration on the platform: the MongoDB of the services, the
// Camunda engines and permissions-v2.
type OldServices struct {
	*Mongo
	*Camunda
	PermissionsV2
}

// Main runs the subcommand migrate; the engine is configured as for serving, the sources by
// flags and, for secrets, by environment variables. It returns the exit code: 0 complete, 1
// partial, 2 not run.
func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.json", "configuration of the engine")
	apply := flags.Bool("apply", false, "write; without it the run only reports")
	asJson := flags.Bool("json", false, "write the report as JSON")
	databases := DefaultMongoDatabases()
	flags.StringVar(&databases.ProcessModels, "db-process-models", databases.ProcessModels, "database of process-model-repository")
	flags.StringVar(&databases.ProcessDeployments, "db-process-deployments", databases.ProcessDeployments, "database of process-deployment")
	flags.StringVar(&databases.Scheduler, "db-scheduler", databases.Scheduler, "database of process-scheduler")
	flags.StringVar(&databases.SchedulerCollection, "collection-scheduler", databases.SchedulerCollection, "collection of process-scheduler")
	flags.StringVar(&databases.Io, "db-io", databases.Io, "database of process-io")
	camundaUrls := flags.String("camunda", "", "comma separated base URLs of the Camunda engines (shards), ending in /engine-rest")
	camundaUser := flags.String("camunda-user", "camunda", "user of the REST API of Camunda; the password is MIGRATE_CAMUNDA_PASSWORD")
	camundaOnly := flags.String("camunda-only", "", "comma separated ids of deployments that exist only in Camunda and are to be migrated")
	owners := flags.String("owner", "", "comma separated old=new owner replacements, e.g. placeholder-for-invalid-data=<user id>")
	noRestart := flags.Bool("no-restart", false, "do not restart running instances, only list them")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	options := Options{Apply: *apply, CamundaOnly: map[string]bool{}, Owners: map[string]string{}, NoRestart: *noRestart}
	for _, id := range strings.Split(*camundaOnly, ",") {
		if id = strings.TrimSpace(id); id != "" {
			options.CamundaOnly[id] = true
		}
	}
	for _, pair := range strings.Split(*owners, ",") {
		if from, to, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			options.Owners[from] = to
		}
	}
	report, err := run(*configPath, options, databases, *camundaUrls, *camundaUser)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "migration not run:", err)
		return 2
	}
	if *asJson {
		_ = json.NewEncoder(stdout).Encode(report)
	} else {
		report.Write(stdout)
	}
	return report.ExitCode()
}

func run(configPath string, options Options, databases MongoDatabases, camundaUrls string, camundaUser string) (*Report, error) {
	config, err := configuration.Load(configPath)
	if err != nil {
		return nil, err
	}
	mongoUrl := os.Getenv("MIGRATE_MONGO_URL")
	if mongoUrl == "" {
		return nil, errors.New("MIGRATE_MONGO_URL is required")
	}
	if config.PermissionsV2Url == "" || config.AuthEndpoint == "" {
		return nil, errors.New("the engine needs permissions_v2_url and auth_endpoint; the migration writes rights and acts as admin")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := config.Logger()
	srv, err := server.New(ctx, config, logger, server.Dependencies{})
	if err != nil {
		return nil, err
	}
	defer srv.Close()
	mongo, err := OpenMongo(ctx, mongoUrl, databases)
	if err != nil {
		return nil, err
	}
	defer mongo.Close(ctx)
	adminToken := auth.ClientCredentials(config.AuthEndpoint, config.AuthClientId, config.AuthClientSecret.Value())
	rights := PermissionsV2{Client: permclient.New(config.PermissionsV2Url), Token: adminToken}
	var urls []string
	for _, u := range strings.Split(camundaUrls, ",") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, strings.TrimSuffix(u, "/"))
		}
	}
	sources := OldServices{Mongo: mongo, Camunda: &Camunda{Urls: urls, User: camundaUser, Password: os.Getenv("MIGRATE_CAMUNDA_PASSWORD")}, PermissionsV2: rights}
	target := &Target{Store: srv.Store, Engine: srv.Engine, Controller: srv.Controller, Namespaces: srv.Namespaces, AdminToken: adminToken, Rights: rights}
	return Run(ctx, options, sources, target), nil
}
