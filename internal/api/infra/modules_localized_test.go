package infra

import "testing"

func TestLocalizedInputs(t *testing.T) {
	for _, m := range Modules() {
		if !m.Available {
			continue
		}
		es := m.Localized("es")
		en := m.Localized("en")
		for i, in := range m.Inputs {
			if in.LabelEN == "" {
				t.Errorf("%s.%s: falta LabelEN", m.Module, in.Name)
			}
			if in.Help != "" && in.HelpEN == "" {
				t.Errorf("%s.%s: falta HelpEN", m.Module, in.Name)
			}
			if es.Inputs[i].Label != in.Label || en.Inputs[i].Label != in.LabelEN {
				t.Errorf("%s.%s: Localized no eligió bien (es=%q en=%q)", m.Module, in.Name, es.Inputs[i].Label, en.Inputs[i].Label)
			}
		}
	}
	// Localized no debe pisar el registro.
	before := Modules()[0].Inputs[0].Label
	_ = LocalizedModules("en")
	if Modules()[0].Inputs[0].Label != before {
		t.Error("LocalizedModules modificó el registro compartido")
	}
}

func TestSslipDomain(t *testing.T) {
	cases := map[string]string{"1.2.3.4": "1-2-3-4.sslip.io", " 203.0.113.9 ": "203-0-113-9.sslip.io", "": "", "no-ip": "", "2001:db8::1": ""}
	for in, want := range cases {
		if got := SslipDomain(in); got != want {
			t.Errorf("SslipDomain(%q) = %q, quería %q", in, got, want)
		}
	}
}
