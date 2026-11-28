package nav

import "testing"

func TestNavigate(t *testing.T) {
	p := Policy{Identity: "stage"}
	dev := Policy{Identity: "stage-dev", DevOrigin: DevOriginOf("http://localhost:5174/")}
	cases := []struct {
		p    Policy
		raw  string
		want Action
		path string
	}{
		{p, "mygo://localhost/", Allow, ""},
		{p, "mygo://localhost/tasks/12?x=1", Allow, ""},
		{p, "MYGO://LOCALHOST/tasks/12", Allow, ""},
		{p, "mygo://other/", Deny, ""},
		{p, "http://localhost:5174/", External, ""},
		{dev, "http://localhost:5174/tasks/3", Allow, ""},
		{dev, "http://localhost:5175/", External, ""},
		{dev, "mygo://localhost/", Allow, ""},
		{p, "https://github.com/flexdinesh/stage/pull/1", External, ""},
		{p, "http://example.com", External, ""},
		{p, "https://user:pw@example.com/", Deny, ""},
		{p, "https:///nohost", Deny, ""},
		{p, "t3code://threads/env_1/thread_2", OpenApp, ""},
		{p, "t3code://threads/env_1", Deny, ""},
		{p, "t3code://settings", Deny, ""},
		{p, "stage://tasks/12", InApp, "/tasks/12"},
		{p, "stage://nope/1", Deny, ""},
		{p, "stage-dev://tasks/12", Deny, ""},
		{dev, "stage-dev://tasks/12", InApp, "/tasks/12"},
		{p, "file:///etc/passwd", Deny, ""},
		{p, "javascript:alert(1)", Deny, ""},
		{p, "data:text/html,<h1>x</h1>", Deny, ""},
		{p, "about:blank", Deny, ""},
		{p, "mailto:a@b.c", Deny, ""},
		{p, "ftp://example.com/", Deny, ""},
	}
	for _, c := range cases {
		d := c.p.Navigate(c.raw)
		if d.Action != c.want || d.Path != c.path {
			t.Errorf("Navigate(%q) = %v %q (%s), want %v %q", c.raw, d.Action, d.Path, d.Reason, c.want, c.path)
		}
	}
}

func TestOpen(t *testing.T) {
	p := Policy{Identity: "stage"}
	dev := Policy{Identity: "stage", DevOrigin: "http://localhost:5174"}
	cases := []struct {
		p    Policy
		raw  string
		want Action
		path string
	}{
		{p, "mygo://localhost/api/attachments/12", Child, ""},
		{p, "mygo://localhost/api/attachments/12?stage-target=local%7C%7C0", Child, ""},
		{dev, "http://localhost:5174/api/attachments/12", Child, ""},
		{p, "mygo://localhost/api/attachments/12?download=1", InApp, "/api/attachments/12?download=1"},
		{p, "mygo://localhost/api/attachments/x", InApp, "/api/attachments/x"},
		{p, "mygo://localhost/tasks/3", InApp, "/tasks/3"},
		{p, "https://example.com/", External, ""},
		{p, "t3code://threads/e/t", OpenApp, ""},
		{p, "stage://sessions/th-1", Deny, ""},
		{p, "file:///tmp/x", Deny, ""},
		{p, "javascript:void(0)", Deny, ""},
	}
	for _, c := range cases {
		d := c.p.Open(c.raw)
		if d.Action != c.want || d.Path != c.path {
			t.Errorf("Open(%q) = %v %q (%s), want %v %q", c.raw, d.Action, d.Path, d.Reason, c.want, c.path)
		}
	}
}

func TestChildNavigate(t *testing.T) {
	p := Policy{Identity: "stage"}
	cur := "mygo://localhost/api/attachments/1"
	for raw, want := range map[string]Action{
		cur:                                  Allow,
		"mygo://localhost/api/attachments/2": Deny,
		"mygo://localhost/":                  Deny,
		"https://example.com/":               External,
		"stage://tasks/1":                    Deny,
		"t3code://threads/e/t":               Deny,
	} {
		if d := p.ChildNavigate(cur, raw); d.Action != want {
			t.Errorf("ChildNavigate(%q) = %v, want %v", raw, d.Action, want)
		}
	}
}

func TestExternalAndApp(t *testing.T) {
	p := Policy{Identity: "stage"}
	for raw, want := range map[string]Action{
		"https://example.com/a?b": External,
		"http://127.0.0.1:7380/":  External,
		"t3code://threads/e/t":    Deny,
		"mygo://localhost/":       Deny,
		"file:///etc/passwd":      Deny,
		"https://u:p@example.com": Deny,
	} {
		if d := p.External(raw); d.Action != want {
			t.Errorf("External(%q) = %v, want %v", raw, d.Action, want)
		}
	}
	for raw, want := range map[string]Action{
		"t3code://threads/e/t": OpenApp,
		"stage://tasks/4":      InApp,
		"https://example.com/": Deny,
		"t3code://evil":        Deny,
		"ms-settings:":         Deny,
	} {
		if d := p.App(raw); d.Action != want {
			t.Errorf("App(%q) = %v, want %v", raw, d.Action, want)
		}
	}
}

func TestDeepLink(t *testing.T) {
	p := Policy{Identity: "stage-dev"}
	cases := map[string]string{
		"stage-dev://":                                           "/",
		"stage-dev:":                                             "/",
		"stage-dev:///":                                          "/",
		"stage-dev://localhost":                                  "/",
		"stage-dev://localhost/":                                 "/",
		"stage-dev://tasks/12":                                   "/tasks/12",
		"stage-dev://tasks/12/":                                  "/tasks/12",
		"STAGE-DEV://Tasks/12":                                   "/tasks/12",
		"stage-dev://localhost/tasks/12":                         "/tasks/12",
		"stage-dev://tasks/12?x=1#y":                             "/tasks/12",
		"stage-dev://trees/4":                                    "/trees/4",
		"stage-dev://localhost/trees/4?task=9":                   "/trees/4",
		"stage-dev://trees/0":                                    "",
		"stage-dev://trees/4/x":                                  "",
		"stage-dev://sessions/thread_abc":                        "",
		"stage-dev://localhost/sessions/a%20b":                   "",
		"stage-dev://sessions/a%2Fb":                             "",
		"stage-dev://tasks/0":                                    "",
		"stage-dev://tasks/-1":                                   "",
		"stage-dev://tasks/12a":                                  "",
		"stage-dev://tasks/1/2":                                  "",
		"stage-dev://tasks":                                      "",
		"stage-dev://sessions/..":                                "",
		"stage-dev://projects/1":                                 "/projects/1",
		"stage-dev://localhost/projects/3?x=1":                   "/projects/3",
		"stage-dev://projects/0":                                 "",
		"stage-dev://orgs/x/projects/3":                          "",
		"stage-dev://localhost/users/x/projects/3":               "",
		"stage-dev://localhost/x/schedules/7":                    "",
		"stage-dev://schedules/7":                                "/schedules/7",
		"stage-dev://schedules/7?entry=4":                        "/schedules/7",
		"stage-dev://schedules/x":                                "",
		"stage-dev://tasks/12?entry=45#journal":                  "/tasks/12?entry=45",
		"stage-dev://trees/4?view=notes&entry=5":                 "/trees/4?entry=5",
		"stage-dev://tasks/12?entry=0":                           "/tasks/12",
		"stage-dev://tasks/12?entry=4%26x=1":                     "/tasks/12",
		"stage-dev://connections/2/sessions/t-1":                 "/connections/2/sessions/t-1",
		"stage-dev://localhost/connections/2/sessions/a%2Fb%20c": "/connections/2/sessions/a%2Fb%20c",
		"stage-dev://connections/0/sessions/t":                   "",
		"stage-dev://connections/2/sessions/..":                  "",
		"stage-dev://connections/2/sessions/%2E":                 "",
		"stage-dev://connections/2/sessions":                     "",
		"stage-dev://connections/2/tasks/1":                      "",
		"stage-dev://connections/2/sessions/a/b":                 "",
		"stage-dev://evil.com/tasks/1":                           "",
		"stage-dev://localhost:80/tasks/1":                       "",
		"stage-dev://u@localhost/tasks/1":                        "",
		"stage://tasks/12":                                       "",
		"https://stage.example/tasks/12":                         "",
		"stage-dev://tasks/99999999999999999999":                 "",
	}
	for raw, want := range cases {
		got, ok := p.DeepLink(raw)
		if got != want || ok != (want != "") {
			t.Errorf("DeepLink(%q) = %q %v, want %q", raw, got, ok, want)
		}
	}
}

func TestT3WebURL(t *testing.T) {
	got, err := T3WebURL("http://t3.example.com:3773/", "t3code://threads/env_1/thread_2")
	if err != nil || got != "http://t3.example.com:3773/env_1/thread_2" {
		t.Fatalf("T3WebURL = %q, %v", got, err)
	}
	if _, err := T3WebURL("http://t3", "t3code://settings"); err == nil {
		t.Error("non-thread link: want error")
	}
	if _, err := T3WebURL("file:///x", "t3code://threads/e/t"); err == nil {
		t.Error("non-http public URL: want error")
	}
}

func TestAlertPath(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"/tasks/12", true},
		{"/tasks/12#answer", false},
		{"/tasks/0", false},
		{"/tasks/12#other", false},
		{"/tasks/12?x=1", false},
		{"/tasks/12/", false},
		{"/tasks/", false},
		{"/trees/3", true},
		{"/trees/3?x=1", false},
		{"/trees/0", false},
		{"/schedules/4", true},
		{"/schedules/4#answer", false},
		{"/schedules/", false},
		{"/projects/1", false},
		{"/sessions/abc", false},
		{"/sessions/abc#answer", false},
		{"/sessions/a%20b", false},
		{"/sessions/a%2Fb", false},
		{"/sessions/a b", false},
		{"/sessions/a/b", false},
		{"/sessions/a%2fb", false},
		{"/sessions/.", false},
		{"/sessions/..", false},
		{"/sessions/%2E%2E", false},
		{"/sessions/", false},
		{"/sessions/%zz", false},
		{"/sessions/a?x=1", false},
		{"/sessions/abc#answer#answer", false},
		{"/settings", true},
		{"/settings#answer", false},
		{"/?inbox", true},
		{"/?inbox=1", false},
		{"/", false},
		{"", false},
		{"https://example.com/tasks/1", false},
		{"//example.com/tasks/1", false},
		{"javascript:alert(1)", false},
		{"mygo://localhost/tasks/1", false},
		{"/api/attachments/1", false},
	}
	for _, c := range cases {
		got, ok := AlertPath(c.in)
		if ok != c.ok || (ok && got != c.in) || (!ok && got != "") {
			t.Errorf("AlertPath(%q) = %q, %v; want ok %v", c.in, got, ok, c.ok)
		}
	}
}
