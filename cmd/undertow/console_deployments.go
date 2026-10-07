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
	"os"
	"strings"

	"undertow/internal/control"
)

func deploymentPrerequisites(method, context string) string {
	switch method {
	case "winrm":
		return "WinRM and WinRS enabled; selected Windows identity authorized for the remote session and ADMIN$"
	case "wmi":
		return "remote WMI process creation allowed; selected Windows identity has target ADMIN$ write access"
	case "service-control":
		return "selected Windows identity has target Service Control Manager and administrative share access; service-capable build; runs as LocalSystem"
	case "scheduled-task":
		if context == "current-user" {
			return "remote Scheduled Task and administrative share access; selected Windows identity has an interactive target session"
		}
		return "selected Windows identity has remote Scheduled Task and administrative share access; task runs as LocalSystem"
	default:
		return "unknown method"
	}
}

func runConsoleDeployment(ctx context.Context, output io.Writer, call consoleCaller, args []string, selectedAgentID string) error {
	if len(args) == 0 {
		return errors.New("use jumps [SOURCE_AGENT]; jump create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT; jump show|prepare|start|link")
	}
	isList := args[0] == "jumps" || args[0] == "deployments" || (args[0] == "jump" || args[0] == "deploy") && len(args) == 2 && args[1] == "list"
	if isList {
		if (args[0] == "jumps" || args[0] == "deployments") && len(args) > 2 {
			return errors.New("use jumps [SOURCE_AGENT]")
		}
		source := selectedAgentID
		if (args[0] == "jumps" || args[0] == "deployments") && len(args) > 1 {
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
			fmt.Fprintln(output, "No Jump records.")
		}
		for _, record := range records {
			fmt.Fprintf(output, "%s  %s  %s  %s  source=%s\n", record.ID, record.State, record.Target, record.Method, record.SourceAgentID)
		}
		return nil
	}
	if args[0] != "jump" && args[0] != "deploy" || len(args) < 2 {
		return errors.New("use jump create|show|prepare|start|link or jumps")
	}
	switch args[1] {
	case "create":
		values := args[2:]
		source := selectedAgentID
		if source == "" {
			if len(values) < 5 {
				return errors.New("use jump create SOURCE_AGENT TARGET ARTIFACT_ID METHOD CONTEXT")
			}
			source, values = values[0], values[1:]
		}
		if len(values) != 4 {
			return errors.New("use jump create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT")
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
		fmt.Fprintf(output, "Created Jump %s for %s using %s. Use jump prepare %s to validate readiness.\n", record.ID, record.Target, record.Method, record.ID)
		return nil
	case "show":
		if len(args) != 3 {
			return errors.New("use jump show ID")
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
			return errors.New("use jump prepare ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/prepare", map[string]any{})
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Jump %s prepared. Source may be sleeping; start queues the Job for its next check-in. Prerequisites: %s. Use jump start %s [INSTALL_PATH].\n", record.ID, deploymentPrerequisites(record.Method, record.Context), record.ID)
		return nil
	case "start":
		if len(args) < 3 {
			return errors.New("use jump start ID [INSTALL_PATH] [--username USER (--password-file FILE | --nt-hash-file FILE)]")
		}
		request := map[string]string{"delivery": "agent-channel"}
		installPath, username, passwordFile, hashFile := "", "", "", ""
		for i := 3; i < len(args); i++ {
			switch args[i] {
			case "--username":
				i++
				if i >= len(args) || username != "" {
					return errors.New("--username requires one Windows account")
				}
				username = args[i]
			case "--password-file":
				i++
				if i >= len(args) || passwordFile != "" {
					return errors.New("--password-file requires one local file")
				}
				passwordFile = args[i]
			case "--nt-hash-file":
				i++
				if i >= len(args) || hashFile != "" {
					return errors.New("--nt-hash-file requires one local file")
				}
				hashFile = args[i]
			default:
				if strings.HasPrefix(args[i], "--") || installPath != "" {
					return errors.New("use jump start ID [INSTALL_PATH] [--username USER (--password-file FILE | --nt-hash-file FILE)]")
				}
				installPath = args[i]
			}
		}
		if installPath != "" {
			request["install_path"] = installPath
		}
		if passwordFile != "" && hashFile != "" {
			return errors.New("choose --password-file or --nt-hash-file")
		}
		if (username == "") != (passwordFile == "" && hashFile == "") {
			return errors.New("use --username with --password-file or --nt-hash-file")
		}
		if passwordFile != "" {
			secret, err := os.ReadFile(passwordFile)
			if err != nil {
				return fmt.Errorf("read Windows password file: %w", err)
			}
			password := strings.TrimRight(string(secret), "\r\n")
			if password == "" {
				return errors.New("Windows password file is empty")
			}
			request["username"], request["password"] = username, password
		}
		if hashFile != "" {
			secret, err := os.ReadFile(hashFile)
			if err != nil {
				return fmt.Errorf("read Windows NT hash file: %w", err)
			}
			hash := strings.TrimSpace(string(secret))
			if hash == "" {
				return errors.New("Windows NT hash file is empty")
			}
			request["username"], request["nt_hash"] = username, hash
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/start", request)
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Jump %s started as Job %s. State: %s.\n", record.ID, record.JobID, record.State)
		return nil
	case "link":
		if len(args) != 4 {
			return errors.New("use jump link ID AGENT_ID")
		}
		data, err := call(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(args[2])+"/link", map[string]string{"agent_id": args[3]})
		if err != nil {
			return err
		}
		var record control.DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		fmt.Fprintf(output, "Jump %s linked to agent %s.\n", record.ID, record.ResultAgentID)
		return nil
	default:
		return fmt.Errorf("unknown jump command %q; use create, show, prepare, start, or link", strings.TrimSpace(args[1]))
	}
}
