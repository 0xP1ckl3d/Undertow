//go:build windows

package windowsdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/FalconOpsLLC/goexec/pkg/goexec"
	"github.com/FalconOpsLLC/goexec/pkg/goexec/dce"
	scmrexec "github.com/FalconOpsLLC/goexec/pkg/goexec/scmr"
	goexecsmb "github.com/FalconOpsLLC/goexec/pkg/goexec/smb"
	tschexec "github.com/FalconOpsLLC/goexec/pkg/goexec/tsch"
	wmiexec "github.com/FalconOpsLLC/goexec/pkg/goexec/wmi"
	"github.com/RedTeamPentesting/adauth"
	"github.com/oiweiwei/go-msrpc/ssp"
	"github.com/oiweiwei/go-msrpc/ssp/gssapi"
)

var hashRPCAuthOnce sync.Once

func hashRPCContext(ctx context.Context) context.Context {
	hashRPCAuthOnce.Do(func() {
		gssapi.AddMechanism(ssp.SPNEGO)
		gssapi.AddMechanism(ssp.NTLM)
	})
	return gssapi.NewSecurityContext(ctx)
}

func hashRPCClient(ctx context.Context, target, endpoint string, useEPM bool, credential HashCredential) (*dce.Client, error) {
	authTarget := adauth.NewTarget("cifs", target)
	client := &dce.Client{Options: dce.Options{
		ClientOptions: goexec.ClientOptions{Host: target},
		AuthOptions: goexec.AuthOptions{
			Target:     authTarget,
			Credential: &adauth.Credential{Username: credential.Username, Domain: credential.Domain, NTHash: credential.NTHash},
		},
		Endpoint: endpoint,
		UseEpm:   useEPM,
	}}
	if err := client.Parse(hashRPCContext(ctx)); err != nil {
		return nil, err
	}
	return client, nil
}

func executeHashMethod(ctx context.Context, method goexec.CleanExecutionMethod, input *goexec.ExecutionIO) (err error) {
	ctx = hashRPCContext(ctx)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Windows management library failed unexpectedly (%T)", recovered)
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if cleanErr := method.Clean(hashRPCContext(cleanCtx)); err == nil && cleanErr != nil {
			err = fmt.Errorf("clean Windows management method: %w", cleanErr)
		}
	}()
	return goexec.ExecuteMethod(ctx, method, input)
}

const cleanupTimeout = 15 * time.Second

func removeTargetFileNTHash(ctx context.Context, target, path string, credential HashCredential) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	ctx = cleanupCtx
	unc, err := targetSharePath(target, path)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(unc, `\\`), `\`)
	if len(parts) < 3 {
		return errors.New("invalid target administrative-share path")
	}
	client := &goexecsmb.Client{ClientOptions: goexecsmb.ClientOptions{
		ClientOptions: goexec.ClientOptions{Host: target},
		AuthOptions: goexec.AuthOptions{
			Target:     adauth.NewTarget("cifs", target),
			Credential: &adauth.Credential{Username: credential.Username, Domain: credential.Domain, NTHash: credential.NTHash},
		},
		NoSeal: true,
	}}
	ctx = hashRPCContext(ctx)
	if err := client.Parse(ctx); err != nil {
		return err
	}
	if err := client.Connect(ctx); err != nil {
		return err
	}
	defer client.Close(ctx)
	share, err := client.Session().Mount(parts[1])
	if err != nil {
		return err
	}
	defer share.Umount()
	if err := share.Remove(strings.Join(parts[2:], `\`)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}

func runNTHash(ctx context.Context, method, target, path, executionContext, shortID string, credential HashCredential, output io.Writer) error {
	input := &goexec.ExecutionIO{Input: &goexec.ExecutionInput{ExecutablePath: path}}
	var err error
	switch method {
	case "winrm":
		return errors.New("WinRM does not support NT-hash authentication")
	case "wmi":
		client, clientErr := hashRPCClient(ctx, target, "", true, credential)
		if clientErr != nil {
			return fmt.Errorf("prepare WMI NT-hash authentication: %w", clientErr)
		}
		module := &wmiexec.WmiProc{Wmi: wmiexec.Wmi{Client: client, Resource: "//./root/cimv2"}}
		err = executeHashMethod(ctx, module, input)
		if err == nil {
			fmt.Fprintln(output, "WMI launch accepted")
		}
	case "service-control":
		client, clientErr := hashRPCClient(ctx, target, scmrexec.DefaultEndpoint, false, credential)
		if clientErr != nil {
			return fmt.Errorf("prepare Service Control NT-hash authentication: %w", clientErr)
		}
		defer func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_ = client.Close(hashRPCContext(cleanCtx))
		}()
		name := "S" + shortID
		module := &scmrexec.ScmrCreate{Scmr: scmrexec.Scmr{Client: client}, NoDelete: true, ServiceName: name, DisplayName: name}
		err = executeHashMethod(ctx, module, input)
		if err == nil {
			fmt.Fprintf(output, "Service %s started\n", name)
		}
	case "scheduled-task":
		if executionContext != "local-system" {
			return errors.New("Scheduled Task with an NT hash requires the LocalSystem context")
		}
		client, clientErr := hashRPCClient(ctx, target, "ncacn_np:[atsvc]", false, credential)
		if clientErr != nil {
			return fmt.Errorf("prepare Scheduled Task NT-hash authentication: %w", clientErr)
		}
		defer func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_ = client.Close(hashRPCContext(cleanCtx))
		}()
		name := "T" + shortID
		module := &tschexec.TschDemand{Tsch: tschexec.Tsch{Client: client, TaskPath: `\` + name, UserSid: "S-1-5-18"}}
		err = executeHashMethod(ctx, module, input)
		if err == nil {
			fmt.Fprintf(output, "Scheduled task %s launched and removed\n", name)
		}
	default:
		return errors.New("unsupported Windows deployment method")
	}
	if err != nil {
		if cleanupErr := removeTargetFileNTHash(ctx, target, path, credential); cleanupErr != nil {
			fmt.Fprintf(output, "Target file cleanup failed: %v\n", cleanupErr)
		} else {
			fmt.Fprintln(output, "Removed target file after launch failure")
		}
	}
	return err
}
