package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"undertow/internal/control"
	"undertow/internal/deployment"
	"undertow/internal/mux"
)

// idleSleep waits for an entirely unused session and asks the server to
// confirm that no route, relay, job, or other remote dependency needs it.
func idleSleep(ctx context.Context, session *mux.Mux, initial control.SleepPolicy) (<-chan time.Duration, func()) {
	ctx, cancelMonitor := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Add(2)
	stop := func() { cancelMonitor(); workers.Wait() }
	sleep := make(chan time.Duration, 1)
	responses := make(chan control.SleepMessage, 8)
	go func() {
		defer workers.Done()
		for {
			data, err := session.RecvControl(ctx)
			if err != nil {
				return
			}
			var message control.SleepMessage
			if json.Unmarshal(data, &message) != nil || message.Kind == "" {
				continue
			}
			select {
			case responses <- message:
			case <-ctx.Done():
				return
			case <-session.Done():
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		policy := initial
		idleSince := time.Now()
		pending := false
		requested := time.Time{}
		cancelRequest := func() {
			if !pending {
				return
			}
			pending = false
			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_ = session.SendControl(requestCtx, control.EncodeSleepMessage("cancel", nil))
			cancel()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-session.Done():
				return
			case message := <-responses:
				switch message.Kind {
				case "policy":
					if message.Policy != nil && message.Policy.Validate() == nil {
						cancelRequest()
						policy = *message.Policy
						idleSince = time.Now()
					}
				case "denied":
					pending = false
					idleSince = time.Now()
				case "granted":
					if pending && policy.IntervalSeconds > 0 && session.StreamCount() == 0 {
						sleep <- deployment.Jitter(time.Duration(policy.IntervalSeconds)*time.Second, policy.JitterPercent)
						return
					}
					requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
					_ = session.SendControl(requestCtx, control.EncodeSleepMessage("cancel", nil))
					cancel()
				}
			case <-ticker.C:
				if policy.IntervalSeconds == 0 || session.StreamCount() != 0 {
					idleSince = time.Now()
					cancelRequest()
					continue
				}
				if pending {
					if time.Since(requested) > 5*time.Second {
						cancelRequest()
						idleSince = time.Now()
					}
					continue
				}
				if time.Since(idleSince) < time.Duration(policy.IntervalSeconds)*time.Second {
					continue
				}
				requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := session.SendControl(requestCtx, control.EncodeSleepMessage("request", nil))
				cancel()
				if err == nil {
					pending = true
					requested = time.Now()
				} else {
					idleSince = time.Now()
				}
			}
		}
	}()
	return sleep, stop
}
