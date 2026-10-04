//go:build linux || windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGUIForwardsBindToOwnClientSession(t *testing.T) {
	var method,path string
	client:=&liveClientConsole{sessionID:705,request:func(_ context.Context,m,p string,_ any)([]byte,error){method,path=m,p;return []byte(`[]`),nil}}
	store,err:=openClientGUIStore(filepath.Join(t.TempDir(),"ui.db"));if err!=nil {t.Fatal(err)};defer store.Close()
	gui:=&guiServer{client:client,store:store,host:"127.0.0.1:9000",sessionSecret:"session",csrfSecret:"csrf"}
	request:=httptest.NewRequest(http.MethodPost,"http://127.0.0.1:9000/api/forwards",strings.NewReader(`{"agent_id":"a","bind":"127.0.0.1:8000","target":"127.0.0.1:9000"}`))
	request.Host="127.0.0.1:9000";request.Header.Set("Origin","http://127.0.0.1:9000");request.Header.Set("Content-Type","application/json");request.Header.Set("X-Undertow-CSRF","csrf");request.AddCookie(&http.Cookie{Name:"undertow_gui",Value:"session"})
	response:=httptest.NewRecorder();gui.handler().ServeHTTP(response,request)
	if response.Code!=http.StatusOK || method!=http.MethodPost || path!="/v1/clients/705/forwards" {t.Fatalf("status=%d method=%s path=%s body=%s",response.Code,method,path,response.Body.String())}
}

func TestGUIOperatorOnlyRoutingModeRefusesLocalRouteMutation(t *testing.T) {
	client:=&liveClientConsole{sessionID:705,operatorOnly:true}
	gui:=&guiServer{client:client,host:"127.0.0.1:9000",sessionSecret:"session",csrfSecret:"csrf"}
	request:=httptest.NewRequest(http.MethodPut,"http://127.0.0.1:9000/api/client/mode/vpn",strings.NewReader(`{"enabled":true}`))
	request.Host="127.0.0.1:9000";request.Header.Set("Origin","http://127.0.0.1:9000");request.Header.Set("Content-Type","application/json");request.Header.Set("X-Undertow-CSRF","csrf");request.AddCookie(&http.Cookie{Name:"undertow_gui",Value:"session"})
	response:=httptest.NewRecorder();gui.handler().ServeHTTP(response,request)
	if response.Code!=http.StatusConflict || client.vpn {t.Fatalf("status=%d vpn=%t",response.Code,client.vpn)}
}

func TestGUIKeepsFullWidthSessionIDs(t *testing.T) {
	data,err:=guiExactSessionIDs([]byte(`{"session_id":11499190048650744901,"agents":[{"session_id":18446744073709551615}],"count":2}`))
	if err!=nil {t.Fatal(err)}
	if got:=string(data);!strings.Contains(got,`"session_id":"11499190048650744901"`) || !strings.Contains(got,`"session_id":"18446744073709551615"`) || !strings.Contains(got,`"count":2`) {t.Fatalf("IDs lost precision: %s",got)}
}
