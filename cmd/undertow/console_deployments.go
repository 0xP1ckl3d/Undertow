//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"undertow/internal/control"
)

func deploymentPrerequisites(method, context string) string {
	switch method {
	case "winrm":
		return "WinRM and PowerShell remoting enabled; source agent identity authorized on the target"
	case "wmi":
		return "remote WMI process creation allowed; hosted delivery also needs target ADMIN$ write access"
	case "service-control":
		return "target Service Control Manager and administrative share access; service-capable build; runs as LocalSystem"
	case "scheduled-task":
		if context == "current-user" {
			return "remote Scheduled Task and administrative share access; source identity has an interactive target session"
		}
		return "remote Scheduled Task and administrative share access; task runs as LocalSystem"
	default:
		return "unknown method"
	}
}

func runConsoleDeployment(ctx context.Context, output io.Writer, call consoleCaller, args []string, selectedAgentID string) error {
	if len(args) == 0 {
		return errors.New("use deployments [SOURCE_AGENT]; deploy create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT; deploy show|prepare|start|link")
	}
	if args[0] == "deployments" || len(args) == 2 && args[1] == "list" {
		if args[0] == "deployments" && len(args) > 2 {
			return errors.New("use deployments [SOURCE_AGENT]")
		}
		source := selectedAgentID
		if args[0] == "deployments" && len(args) > 1 {
			source = args[1]
		}
		path := "/v1/deployments"
		if source != "" {
			path += "?source_agent_id=" + url.QueryEscape(source)
		}
		data, err := call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var records []control.DeploymentRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		if len(records) == 0 {
			fmt.Fprintln(output, "No deployment records.")
		}
		for _, record := range records {
			fmt.Fprintf(output, "%s  %s  %s  %s  source=%s\n", record.ID, record.State, record.Target, record.Method, record.SourceAgentID)
		}
		return nil
	}
	if args[0] != "deploy" || len(args) < 2 {
		return errors.New("use deploy create|show|prepare|start|link or deployments")
	}
	switch args[1] {
	case "create":
		values := args[2:]
		source := selectedAgentID
		if source == "" {
			if len(values) < 5 {
				return errors.New("use deploy create SOURCE_AGENT TARGET ARTIFACT_ID METHOD CONTEXT")
			}
			source, values = values[0], values[1:]
		}
		if len(values) != 4 {
			return errors.New("use deploy create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT")
		}
		request := map[string]string{"source_agent_id": source, "target": values[0], "artifact_id": values[1], "method": values[2], "context": values[3]}
		data, err := call(ctx, http.MethodPost, "/v1/deployments", request)
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Created deployment %s for %s using %s. Use deploy prepare %s to validate readiness.\n", record.ID, record.Target, record.Method, record.ID)
		return nil
	case "show":
		if len(args) != 3 {
			return errors.New("use deploy show ID")
		}
		data, err := call(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(args[2]), nil)
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		operator := record.OperatorName
		if operator == "" {
			operator = record.OperatorID
		}
		if operator == "" {
			operator = record.RequestedFrom
		}
		fmt.Fprintf(output, "ID: %s\nState: %s\nSource: %s\nTarget: %s\nArtifact: %s (%s)\nProfile: %s (%s)\nMethod: %s\nContext: %s\nOperator: %s\nCreated: %s\nUpdated: %s\nProgress: %s\n", record.ID, record.State, record.SourceAgentID, record.Target, record.ArtifactID, record.ArtifactSHA256, record.Profile, record.ProfileID, record.Method, record.Context, operator, record.CreatedAt.Local().Format("2006-01-02 15:04:05"), record.UpdatedAt.Local().Format("2006-01-02 15:04:05"), record.Progress)
		fmt.Fprintf(output, "Prerequisites: %s\n", deploymentPrerequisites(record.Method, record.Context))
		if record.Account != "" {
			fmt.Fprintf(output, "Account: %s\n", record.Account)
		}
		if record.Error != "" {
			fmt.Fprintf(output, "Error: %s\n", record.Error)
		}
		if record.JobID != "" {
			fmt.Fprintf(output, "Job: %s\n", record.JobID)
		}
		if record.TransferID != "" {
			fmt.Fprintf(output, "Transfer: %s\n", record.TransferID)
		}
		if record.DeliveryType != "" {
			fmt.Fprintf(output, "Delivery: %s (%s)\n", record.DeliveryType, record.DeliveryID)
		}
		if record.InstallPath != "" {
			fmt.Fprintf(output, "Install path: %s\n", record.InstallPath)
		}
		if record.ServiceName != "" {
			fmt.Fprintf(output, "Service: %s\n", record.ServiceName)
		}
		if record.TaskName != "" {
			fmt.Fprintf(output, "Task: %s\n", record.TaskName)
		}
		if record.ResultAgentID != "" {
			fmt.Fprintf(output, "Resulting agent: %s (%s)\n", record.ResultAgentID, record.ResultRelationship)
		}
		return nil
	case "prepare":
		if len(args) != 3 {
			return errors.New("use deploy prepare ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/prepare", map[string]any{})
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Deployment %s prepared. Prerequisites: %s. Use deploy start %s direct-share|server|agent-host:HOST_ID [INSTALL_PATH].\n", record.ID, deploymentPrerequisites(record.Method, record.Context), record.ID)
		return nil
	case "start":
		if len(args) < 4 || len(args) > 5 {
			return errors.New("use deploy start ID direct-share|server|agent-host:HOST_ID [INSTALL_PATH]")
		}
		request := map[string]string{"delivery": args[3]}
		if len(args) == 5 {
			request["install_path"] = args[4]
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/start", request)
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Deployment %s started as Job %s. State: %s.\n", record.ID, record.JobID, record.State)
		return nil
	case "link":
		if len(args) != 4 {
			return errors.New("use deploy link ID AGENT_ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/link", map[string]string{"agent_id": args[3]})
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Deployment %s linked to agent %s.\n", record.ID, record.ResultAgentID)
		return nil
	default:
		return fmt.Errorf("unknown deploy command %q; use create, show, prepare, start, or link", strings.TrimSpace(args[1]))
	}
}
