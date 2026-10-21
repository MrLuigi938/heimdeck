package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// openmediavault: the JSON-RPC interface of the web UI (/rpc.php), the only
// API it has (github.com/openmediavault/openmediavault, engined/rpc). It needs
// an administrator; the session ID from Session.login is sent along in the
// X-OpenMediaVault-SessionID header.

type openmediavault struct{}

func init() { register(openmediavault{}) }

func (openmediavault) Def() Definition {
	return Definition{
		ID: "openmediavault", Name: "openmediavault", Icon: "openmediavault", Category: "System",
		Description: "CPU, RAM, Laufzeit, Updates, Belegung der Dateisysteme und Zustand der Festplatten (S.M.A.R.T.)",
		Setup: []string{
			"Als lokale Adresse die Adresse der openmediavault-Weboberfläche eintragen, z. B. http://<IP>.",
			"Benutzer und Passwort eines Administrators angeben (z. B. admin). Die Schnittstelle von openmediavault gibt die Systemwerte nur Administratoren heraus.",
			"Ist für den Benutzer die Zwei-Faktor-Anmeldung aktiv, klappt die Anmeldung nicht – dann einen eigenen Administrator ohne 2FA für Heimdeck anlegen.",
		},
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true, Help: "Ein Administrator, z. B. admin"},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
		},
		HasWidget: true, Images: []string{"openmediavault"}, Port: 80,
		Refresh: 60,
	}
}

// omvSessions holds the session ID per address and user, so polling does not
// log in every time (and does not fill the log with logins).
var omvSessions sync.Map

type omvError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (o openmediavault) rpc(ctx context.Context, in *Instance, service, method string, params, out any) error {
	key := in.BaseURL + "\x00" + in.V("username")
	for attempt := 0; attempt < 2; attempt++ {
		sid, _ := omvSessions.Load(key)
		if sid == nil {
			s, err := o.login(ctx, in)
			if err != nil {
				return err
			}
			omvSessions.Store(key, s)
			sid = s
		}
		var res struct {
			Response json.RawMessage `json:"response"`
			Error    *omvError       `json:"error"`
		}
		err := in.do(ctx, request{method: "POST", path: "/rpc.php",
			headers: map[string]string{"X-OpenMediaVault-SessionID": sid.(string)},
			body: map[string]any{"service": service, "method": method, "params": params,
				"options": map[string]any{"updatelastaccess": false}}}, &res)
		var he *HTTPError
		if errors.As(err, &he) && he.Status == 401 && attempt == 0 {
			omvSessions.Delete(key) // session expired: log in again
			continue
		}
		if err != nil {
			return err
		}
		if res.Error != nil {
			return in.Errorf("openmediavault meldet: %s", res.Error.Message)
		}
		return json.Unmarshal(res.Response, out)
	}
	return in.Errorf("Anmeldung fehlgeschlagen")
}

func (openmediavault) login(ctx context.Context, in *Instance) (string, error) {
	var res struct {
		Response *struct {
			Status    string `json:"status"`
			SessionID string `json:"sessionid"`
		} `json:"response"`
		Error *omvError `json:"error"`
	}
	err := in.do(ctx, request{method: "POST", path: "/rpc.php", body: map[string]any{
		"service": "Session", "method": "login",
		"params": map[string]string{"username": in.V("username"), "password": in.Values["password"]}}}, &res)
	var he *HTTPError
	switch {
	case errors.As(err, &he) && he.Status == 400:
		return "", in.Errorf("Benutzername oder Passwort falsch")
	case errors.As(err, &he) && he.Status == 429:
		return "", in.Errorf("Zu viele Fehlversuche – openmediavault sperrt die Anmeldung kurz")
	case err != nil:
		return "", err
	case res.Error != nil:
		return "", in.Errorf("openmediavault meldet: %s", res.Error.Message)
	case res.Response == nil:
		return "", in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "openmediavault")
	case res.Response.Status == "challengeRequired":
		return "", in.Errorf("Für diesen Benutzer ist die Zwei-Faktor-Anmeldung aktiv – bitte einen Administrator ohne 2FA verwenden")
	case res.Response.SessionID == "":
		return "", in.Errorf("Anmeldung fehlgeschlagen")
	}
	return res.Response.SessionID, nil
}

func (o openmediavault) Test(ctx context.Context, in *Instance) error {
	var info omvInfo
	return o.rpc(ctx, in, "System", "getInformation", nil, &info)
}

// omvNum reads numbers that openmediavault sends as strings (bytes > 4 GiB).
type omvNum float64

func (n *omvNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	*n = omvNum(f)
	return err
}

type omvInfo struct {
	Hostname            string `json:"hostname"`
	Version             string `json:"version"`
	CPUUtilization      omvNum `json:"cpuUtilization"` // percent
	MemUtilization      omvNum `json:"memUtilization"` // 0..1
	Uptime              omvNum `json:"uptime"`         // seconds
	RebootRequired      bool   `json:"rebootRequired"`
	ConfigDirty         bool   `json:"configDirty"`
	AvailablePkgUpdates int    `json:"availablePkgUpdates"`
}

func uptimeText(in *Instance, seconds float64) string {
	if d := int(seconds / 86400); d > 0 {
		return in.T("%d Tage", d)
	}
	return in.T("%d Std.", int(seconds/3600))
}

func (o openmediavault) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var info omvInfo
	if err := o.rpc(ctx, in, "System", "getInformation", nil, &info); err != nil {
		return nil, err
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("CPU"), Value: fmt.Sprintf("%.0f %%", float64(info.CPUUtilization)), Tone: toneIf(info.CPUUtilization > 90, "warn")},
		{Label: in.T("RAM"), Value: fmt.Sprintf("%.0f %%", float64(info.MemUtilization)*100), Tone: toneIf(info.MemUtilization > 0.9, "warn")},
		{Label: in.T("Laufzeit"), Value: uptimeText(in, float64(info.Uptime))},
		{Label: in.T("Updates"), Value: in.Num(info.AvailablePkgUpdates), Tone: toneIf(info.AvailablePkgUpdates > 0, "warn")},
	}}
	if info.RebootRequired {
		w.Alerts = append(w.Alerts, in.T("Neustart erforderlich"))
	}
	if info.ConfigDirty {
		w.Alerts = append(w.Alerts, in.T("Geänderte Einstellungen sind noch nicht übernommen"))
	}

	var fss []struct {
		DeviceFile string `json:"devicefile"`
		Label      string `json:"label"`
		Type       string `json:"type"`
		MountPoint string `json:"mountpoint"`
		Size       omvNum `json:"size"`
		Used       omvNum `json:"used"`
		Available  omvNum `json:"available"`
		Percentage int    `json:"percentage"`
	}
	if err := o.rpc(ctx, in, "FileSystemMgmt", "enumerateMountedFilesystems", map[string]bool{"includeroot": true}, &fss); err != nil {
		return nil, err
	}
	sort.SliceStable(fss, func(i, j int) bool { return fss[i].Percentage > fss[j].Percentage })
	for _, fs := range fss {
		if fs.Size <= 0 {
			continue
		}
		name := firstNonEmpty(fs.Label, fs.MountPoint, fs.DeviceFile)
		if fs.MountPoint == "/" {
			name = in.T("System")
		}
		p := float64(fs.Percentage)
		w.Items = append(w.Items, Item{Title: name,
			Subtitle: in.T("%s frei von %s", in.Bytes(float64(fs.Available)), in.Bytes(float64(fs.Size))),
			Badge:    fmt.Sprintf("%d %%", fs.Percentage), Progress: pct(p)})
		if fs.Percentage >= 90 {
			w.Alerts = append(w.Alerts, in.T("„%s“ ist zu %d %% voll", name, fs.Percentage))
		}
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Dateisysteme")
		w.Items = w.Items[:min(5, len(w.Items))]
	}

	// S.M.A.R.T. is optional: some disks (USB, virtual) do not support it.
	var disks []struct {
		DeviceFile    string `json:"devicefile"`
		Model         string `json:"model"`
		Temperature   any    `json:"temperature"`
		OverallStatus string `json:"overallstatus"`
	}
	if o.rpc(ctx, in, "Smart", "enumerateDevices", nil, &disks) == nil && len(disks) > 0 {
		bad := 0
		for _, d := range disks {
			if d.OverallStatus != "" && d.OverallStatus != "GOOD" {
				bad++
				w.Alerts = append(w.Alerts, in.T("Festplatte %s: S.M.A.R.T.-Warnung", firstNonEmpty(d.Model, d.DeviceFile)))
			}
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Platten OK"), Value: fmt.Sprintf("%d/%d", len(disks)-bad, len(disks)), Tone: toneIf(bad > 0, "bad")})
	}
	return w, nil
}
