//go:build linux || windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/mux"
)

type windowsDeploymentExecutor struct {
	manager      *control.Manager
	distribution *agentDistribution
}

type windowsDeploymentEndpoint struct {
	artifactPath string
}

var windowsInstallPath = regexp.MustCompile(`^[A-Za-z]:\\[^\x00-\x1f"<>:|?*]+\.exe$`)

func randomWindowsPathID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate deployment path: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func normalizeWindowsInstallPath(requested string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(requested, "/", `\`))
	if value == "" {
		id, err := randomWindowsPathID()
		if err != nil {
			return "", err
		}
		value = `C:\Windows\Temp\` + id + `.exe`
	}
	if len(value) > 1024 || !windowsInstallPath.MatchString(value) {
		return "", errors.New("install path must be an absolute Windows .exe path")
	}
	for _, part := range strings.Split(value[3:], `\`) {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", errors.New("install path contains an invalid Windows path component")
		}
	}
	return value, nil
}

func (e *windowsDeploymentExecutor) Preflight(_ context.Context, record control.DeploymentRecord, request control.DeploymentStartRequest) (control.DeploymentExecutionPlan, error) {
	a, err := e.distribution.store.Artifact(record.ArtifactID)
	if err != nil || a.ID != record.ArtifactID || a.Revoked || a.Platform != "windows" || !strings.EqualFold(a.SHA256, record.ArtifactSHA256) {
		return control.DeploymentExecutionPlan{}, errors.New("selected Windows artifact is unavailable or changed")
	}
	artifactPath := e.distribution.store.ArtifactPath(a)
	if err := agentprofile.VerifyFile(artifactPath, record.ArtifactSHA256); err != nil {
		return control.DeploymentExecutionPlan{}, errors.New("selected Windows artifact failed integrity validation")
	}
	if record.Method == "service-control" && !a.ServiceCapable {
		return control.DeploymentExecutionPlan{}, errors.New("Service Control requires a service-capable Windows artifact")
	}
	if record.Context == "named-account" || record.Account != "" {
		return control.DeploymentExecutionPlan{}, errors.New("named-account deployments require a future credential integration")
	}
	path, err := normalizeWindowsInstallPath(request.InstallPath)
	if err != nil {
		return control.DeploymentExecutionPlan{}, err
	}
	selection := strings.TrimSpace(request.Delivery)
	if selection != "" && selection != "agent-channel" && selection != "direct-share" {
		return control.DeploymentExecutionPlan{}, errors.New("Windows deployments stream the artifact through the source agent channel")
	}
	return control.DeploymentExecutionPlan{DeliveryType: "agent-channel", DeliveryID: record.SourceAgentID, InstallPath: path, Opaque: windowsDeploymentEndpoint{artifactPath: artifactPath}}, nil
}

func (e *windowsDeploymentExecutor) Start(ctx context.Context, record control.DeploymentRecord, plan control.DeploymentExecutionPlan, source *mux.Mux, existingJobID string, report func(control.DeploymentProgress) error) error {
	endpoint, ok := plan.Opaque.(windowsDeploymentEndpoint)
	if !ok {
		return errors.New("Windows deployment endpoint is unavailable")
	}
	sharePath, err := windowsAdminSharePath(record.Target, plan.InstallPath)
	if err != nil {
		return err
	}
	transfer, history, err := e.manager.StreamDeploymentArtifact(ctx, record.SourceAgentID, source, endpoint.artifactPath, sharePath)
	transferID := history.ID
	if err != nil {
		if transferID != "" {
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Artifact stream failed", Failure: "Artifact stream failed: " + trimDeploymentError(err), TransferID: transferID, InstallPath: plan.InstallPath})
		}
		return fmt.Errorf("stream artifact through source agent to target share: %w", err)
	}
	if !strings.EqualFold(transfer.SHA256, record.ArtifactSHA256) {
		_ = report(control.DeploymentProgress{State: "failed", Progress: "Artifact stream failed", Failure: "The completed Transfer checksum did not match the selected build.", TransferID: transferID, InstallPath: plan.InstallPath})
		return errors.New("streamed artifact checksum did not match the selected build")
	}
	argv, serviceName, taskName, err := buildWindowsDeploymentCommand(record, plan.InstallPath)
	if err != nil {
		_ = report(control.DeploymentProgress{State: "failed", Progress: "Method preparation failed", Failure: err.Error(), TransferID: transferID, InstallPath: plan.InstallPath})
		return err
	}
	job, err := e.manager.StartDeploymentCommandJob(ctx, record.SourceAgentID, record.ID, existingJobID, argv)
	if err != nil {
		if transferID != "" {
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Method Job start failed", Failure: "Windows method Job could not start after artifact transfer: " + trimDeploymentError(err), TransferID: transferID, InstallPath: plan.InstallPath})
		}
		return err
	}
	progress := control.DeploymentProgress{State: "waiting", Progress: "Native Windows method Job accepted; waiting for target enrolment", JobID: job.ID, TransferID: transferID, InstallPath: plan.InstallPath, ServiceName: serviceName, TaskName: taskName}
	if err := report(progress); err != nil {
		return err
	}
	go e.monitor(record.ID, job.ID, report)
	return nil
}

func (e *windowsDeploymentExecutor) monitor(deploymentID string, jobID string, report func(control.DeploymentProgress) error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		job, err := e.manager.Job(0, jobID, true)
		if err != nil {
			return
		}
		switch job.State {
		case "completed":
			_ = report(control.DeploymentProgress{State: "waiting", Progress: "Native Windows method completed; waiting for target enrolment", JobID: job.ID})
			return
		case "failed":
			failure := strings.TrimSpace(job.OutputError)
			if failure == "" {
				failure = strings.TrimSpace(job.Output)
			}
			if failure == "" {
				failure = "The source-agent Job failed without diagnostic output."
			}
			if len(failure) > 1024 {
				failure = failure[len(failure)-1024:]
			}
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Windows method failed", Failure: "Source-agent Job failed: " + failure, JobID: jobID})
			return
		case "cancelled":
			_ = report(control.DeploymentProgress{State: "failed", Progress: "Windows method cancelled", Failure: "The linked source-agent Job was cancelled.", JobID: jobID})
			return
		case "interrupted":
			_ = report(control.DeploymentProgress{State: "waiting", Progress: "Windows method Job was interrupted; execution outcome is uncertain. Review before retrying.", JobID: jobID})
			return
		}
	}
}

func trimDeploymentError(err error) string {
	if err == nil {
		return "unknown error"
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 768 {
		value = value[len(value)-768:]
	}
	return value
}

func windowsAdminSharePath(target, installPath string) (string, error) {
	if !windowsInstallPath.MatchString(installPath) {
		return "", errors.New("invalid Windows install path")
	}
	const windowsRoot = `C:\Windows\`
	if len(installPath) >= len(windowsRoot) && strings.EqualFold(installPath[:len(windowsRoot)], windowsRoot) {
		return `\\` + target + `\ADMIN$\` + installPath[len(windowsRoot):], nil
	}
	return `\\` + target + `\` + strings.ToUpper(installPath[:1]) + `$\` + installPath[3:], nil
}

func buildWindowsDeploymentCommand(record control.DeploymentRecord, installPath string) ([]string, string, string, error) {
	if !windowsInstallPath.MatchString(installPath) || strings.ContainsAny(installPath, `&^%!()`) {
		return nil, "", "", errors.New("install path contains characters that are unsafe for native Windows management tools")
	}
	shortID := record.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	quotedPath := `"` + installPath + `"`
	switch record.Method {
	case "winrm":
		command := `start "" ` + quotedPath + ` && echo Undertow WinRM launch accepted`
		return []string{"winrs.exe", "-r:" + record.Target, "cmd.exe", "/d", "/s", "/c", command}, "", "", nil
	case "wmi":
		return []string{"wmic.exe", "/node:" + record.Target, "process", "call", "create", quotedPath}, "", "", nil
	case "service-control":
		serviceName := "Undertow-" + shortID
		command := `sc.exe \\` + record.Target + ` create ` + serviceName + ` binPath= "\"` + installPath + `\"" start= auto obj= LocalSystem && sc.exe \\` + record.Target + ` start ` + serviceName
		return []string{"cmd.exe", "/d", "/s", "/c", command}, serviceName, "", nil
	case "scheduled-task":
		taskName := "Undertow-Deploy-" + shortID
		runAs := "SYSTEM"
		extra := ""
		if record.Context == "current-user" {
			runAs = `%USERDOMAIN%\%USERNAME%`
			extra = " /IT /NP"
		}
		command := `schtasks.exe /Create /S ` + record.Target + ` /TN ` + taskName + ` /SC ONCE /ST 00:00 /TR "\"` + installPath + `\"" /RU "` + runAs + `"` + extra + ` /RL HIGHEST /F && schtasks.exe /Run /S ` + record.Target + ` /TN ` + taskName + ` && ping.exe -n 3 127.0.0.1 >NUL && schtasks.exe /Delete /S ` + record.Target + ` /TN ` + taskName + ` /F`
		return []string{"cmd.exe", "/d", "/s", "/c", command}, "", taskName, nil
	default:
		return nil, "", "", errors.New("unsupported Windows deployment method")
	}
}
