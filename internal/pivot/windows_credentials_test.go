package pivot

import "testing"

func TestNewWindowsCredentialForms(t *testing.T) {
	tests := []struct {
		account string
		want    string
	}{
		{"alice", `app01\alice`},
		{`LAB\alice`, `LAB\alice`},
		{"alice@example.com", "alice@example.com"},
	}
	for _, test := range tests {
		credential, err := NewWindowsCredential("app01.example.com", test.account, "password")
		if err != nil || credential.Account() != test.want || credential.Password != "password" {
			t.Fatalf("account %q: credential=%+v err=%v", test.account, credential, err)
		}
	}
}

func TestNewWindowsCredentialLocalUserWithIPTarget(t *testing.T) {
	credential, err := NewWindowsCredential("192.168.50.25", "operator", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Domain != "192.168.50.25" || credential.Username != "operator" || credential.Account() != `192.168.50.25\operator` {
		t.Fatalf("unexpected credential: %#v", credential)
	}
}

func TestNewWindowsCredentialValidation(t *testing.T) {
	if credential, err := NewWindowsCredential("app01", "", ""); err != nil || credential != nil {
		t.Fatalf("empty credential=%+v err=%v", credential, err)
	}
	for _, values := range [][2]string{{"alice", ""}, {"", "password"}, {`LAB\\alice`, "password"}, {"alice@@example.com", "password"}, {"alice", "bad\x00password"}} {
		if _, err := NewWindowsCredential("app01", values[0], values[1]); err == nil {
			t.Fatalf("accepted invalid credential %#v", values)
		}
	}
}

func TestNewWindowsHashCredential(t *testing.T) {
	credential, err := NewWindowsHashCredential("app01", `LAB\operator`, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Username != "operator" || credential.Domain != "LAB" || credential.NTHash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || credential.Password != "" || !credential.UsesNTHash() {
		t.Fatalf("credential=%+v", credential)
	}
	upn, err := NewWindowsHashCredential("app01", "operator@lab.example", "00000000000000000000000000000000:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	if err != nil || upn.Username != "operator" || upn.Domain != "lab.example" || upn.NTHash != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("UPN credential=%+v err=%v", upn, err)
	}
	for _, value := range []string{"short", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "0000000000000000000000000000000000", "bad:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "a:b:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"} {
		if _, err := NewWindowsHashCredential("app01", "operator", value); err == nil {
			t.Fatalf("accepted invalid NT hash %q", value)
		}
	}
	if _, err := NewWindowsCredentialSecret("app01", "operator", "password", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("accepted both password and NT hash")
	}
}
