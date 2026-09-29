package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestSessions() *Sessions {
	return NewSessions([]byte(strings.Repeat("k", 32)), time.Hour, false)
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTestSessions()
	value, exp := s.Issue("user-1")
	if time.Until(exp) < 59*time.Minute {
		t.Errorf("vencimiento demasiado corto: %v", exp)
	}
	got, err := s.Verify(value)
	if err != nil || got != "user-1" {
		t.Fatalf("Verify: %q, %v", got, err)
	}
}

func TestSessionTamper(t *testing.T) {
	s := newTestSessions()
	value, _ := s.Issue("user-1")

	// Cambio un byte del payload: la firma deja de coincidir.
	payload, sig, _ := strings.Cut(value, ".")
	tampered := payload[:len(payload)-1] + "A" + "." + sig
	if tampered == value {
		tampered = payload[:len(payload)-1] + "B" + "." + sig
	}
	if _, err := s.Verify(tampered); !errors.Is(err, ErrBadSession) {
		t.Errorf("payload alterado debería fallar con ErrBadSession, tengo %v", err)
	}

	// Otro secreto: firma inválida.
	other := NewSessions([]byte(strings.Repeat("z", 32)), time.Hour, false)
	if _, err := other.Verify(value); !errors.Is(err, ErrBadSession) {
		t.Errorf("otro secreto debería fallar, tengo %v", err)
	}

	for _, bad := range []string{"", "sin-punto", ".", "a.b", "!!!.xyz"} {
		if _, err := s.Verify(bad); err == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
}

func TestSessionExpired(t *testing.T) {
	s := newTestSessions()
	value, _ := s.Issue("user-1")
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.Verify(value); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("esperaba ErrSessionExpired, tengo %v", err)
	}
}

func TestSessionCookie(t *testing.T) {
	s := newTestSessions()
	rec := httptest.NewRecorder()
	s.SetCookie(rec, "user-9")
	res := rec.Result()
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == SessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no se seteó la cookie")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("atributos de la cookie: %+v", cookie)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	uid, err := s.UserFromRequest(req)
	if err != nil || uid != "user-9" {
		t.Fatalf("UserFromRequest: %q, %v", uid, err)
	}

	rec = httptest.NewRecorder()
	s.ClearCookie(rec)
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie && c.MaxAge >= 0 {
			t.Errorf("ClearCookie debería vencerla: %+v", c)
		}
	}
}

func TestStateSignature(t *testing.T) {
	s := newTestSessions()
	signed := s.SignState("abc123")
	got, err := s.VerifyState(signed)
	if err != nil || got != "abc123" {
		t.Fatalf("VerifyState: %q, %v", got, err)
	}
	if _, err := s.VerifyState(signed + "x"); err == nil {
		t.Error("state alterado debería fallar")
	}
}

func TestTokens(t *testing.T) {
	plain, hash, err := NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "rampa_") || IsClusterToken(plain) || !IsAPIToken(plain) {
		t.Errorf("token de API con forma inesperada: %s", plain)
	}
	if len(hash) != 64 || hash != HashToken(plain) {
		t.Errorf("hash inesperado: %s", hash)
	}
	if strings.Contains(hash, plain[len("rampa_"):]) {
		t.Error("el hash no puede contener el token")
	}

	cplain, chash, err := NewClusterToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cplain, "rampa_cluster_") || !IsClusterToken(cplain) || IsAPIToken(cplain) {
		t.Errorf("token de cluster con forma inesperada: %s", cplain)
	}
	if chash == hash {
		t.Error("dos tokens distintos no pueden tener el mismo hash")
	}

	p2, _, _ := NewAPIToken()
	if p2 == plain {
		t.Error("dos tokens generados deberían ser distintos")
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]struct {
		tok string
		ok  bool
	}{
		"Bearer rampa_abc": {"rampa_abc", true},
		"bearer rampa_abc": {"rampa_abc", true},
		"Bearer ":          {"", false},
		"Basic abc":        {"", false},
		"":                 {"", false},
	}
	for in, want := range cases {
		tok, ok := BearerToken(in)
		if tok != want.tok || ok != want.ok {
			t.Errorf("BearerToken(%q) = %q, %v; quería %q, %v", in, tok, ok, want.tok, want.ok)
		}
	}
}

func TestRoles(t *testing.T) {
	if !RoleOwner.AtLeast(RoleAdmin) || !RoleAdmin.AtLeast(RoleAdmin) || RoleDeveloper.AtLeast(RoleAdmin) {
		t.Error("orden de roles incorrecto")
	}
	if !RoleViewer.AtLeast(RoleViewer) || RoleViewer.AtLeast(RoleDeveloper) {
		t.Error("viewer mal ubicado")
	}
	if Role("dios").AtLeast(RoleViewer) {
		t.Error("un rol desconocido no alcanza nada")
	}
	if _, err := ParseRole("root"); err == nil {
		t.Error("ParseRole debería rechazar roles desconocidos")
	}
	if r, err := ParseRole("developer"); err != nil || r != RoleDeveloper {
		t.Errorf("ParseRole(developer) = %v, %v", r, err)
	}
	if RequiredRoleForScope("prod") != RoleAdmin || RequiredRoleForScope("dev") != RoleDeveloper || RequiredRoleForScope("pr-12") != RoleDeveloper {
		t.Error("rol requerido por scope incorrecto")
	}
}

func TestCheckRole(t *testing.T) {
	ctx := WithOrg(t.Context(), &OrgContext{Role: RoleDeveloper})
	if err := CheckRole(ctx, RoleDeveloper); err != nil {
		t.Errorf("developer debería alcanzar developer: %v", err)
	}
	var fe *ForbiddenError
	if err := CheckRole(ctx, RoleAdmin); !errors.As(err, &fe) {
		t.Errorf("developer no debería alcanzar admin: %v", err)
	}
	if err := CheckRole(t.Context(), RoleViewer); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("sin org debería ser unauthorized: %v", err)
	}
}

func TestMinAndAbove(t *testing.T) {
	if Min(RoleAdmin, RoleDeveloper) != RoleDeveloper || Min(RoleViewer, RoleOwner) != RoleViewer || Min(RoleAdmin, RoleAdmin) != RoleAdmin {
		t.Error("Min incorrecto")
	}
	if Min(Role("raro"), RoleOwner) != Role("raro") {
		t.Error("un rol inválido debe contar como el menor")
	}
	above := Above(RoleDeveloper)
	got := map[string]bool{}
	for _, r := range above {
		got[r] = true
	}
	if len(above) != 2 || !got["admin"] || !got["owner"] {
		t.Errorf("Above(developer) = %v", above)
	}
	if len(Above(RoleOwner)) != 0 {
		t.Error("nada está por encima de owner")
	}
}
