package integrations

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera"
)

// camera shows an IP camera: a still picture with ▶ for the live stream
// (RTSP, H.264/H.265), and its latest events – from ONVIF, or noticed in the
// stream itself. The video goes through Heimdeck (package camera); the
// browser never talks to the camera.

type cameraTile struct{}

func init() { register(cameraTile{}) }

func (cameraTile) Def() Definition {
	return Definition{
		ID: "camera", Name: "Kamera (RTSP/ONVIF)", Icon: "📹", Category: "Smart Home",
		Description: "Livebild und Ereignisse einer Überwachungskamera – über RTSP und/oder ONVIF, ohne Cloud",
		NoURL:       true, HasWidget: true, Refresh: 20,
		Setup: []string{
			"In der App oder Weboberfläche der Kamera RTSP einschalten (oft unter Netzwerk, Erweitert oder „NVR/RTSP“) und die RTSP-Adresse notieren, z. B. rtsp://192.168.1.50:554/live.",
			"Viele Kameras haben einen zweiten, kleineren Stream (Substream) – der lädt schneller und schont das Netz.",
			"Kann die Kamera ONVIF, reicht auch nur deren Adresse: Heimdeck findet Stream, Standbild und Ereignisse dann selbst.",
			"Akku-Kameras: das Livebild und „Bewegung aus dem Stream erkennen“ halten die Kamera wach und leeren den Akku.",
		},
		Fields: []Field{
			{Key: "rtsp", Label: "RTSP-Adresse", Type: "text",
				Help: "z. B. rtsp://192.168.1.50:554/live – leer lassen, wenn ONVIF die Adresse liefern soll."},
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Das Kamera-Konto für RTSP/ONVIF (nicht das Cloud-Konto der App)."},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
			{Key: "onvif", Label: "ONVIF-Adresse (optional)", Type: "text",
				Help: "z. B. http://192.168.1.50:8000 – für Ereignisse der Kamera (Bewegung, Person, Klingel …) und das Standbild."},
			{Key: "snapshot", Label: "Standbild-Adresse (optional)", Type: "text",
				Help: "Eine Adresse, unter der die Kamera ein JPEG liefert. Ohne sie nimmt Heimdeck ein Bild aus dem Stream."},
			{Key: "events", Label: "Ereignisse", Type: "select", Options: []string{"automatisch", "aus dem Stream erkennen", "aus"},
				Help: "„automatisch“: die Ereignisse der Kamera über ONVIF, wenn eingetragen. „aus dem Stream erkennen“: Heimdeck erkennt Bewegung selbst – für Kameras, die nur RTSP können; der Stream läuft dann dauernd."},
			{Key: "sensitivity", Label: "Empfindlichkeit", Type: "select", Options: []string{"mittel", "niedrig", "hoch"},
				Help: "Nur für „aus dem Stream erkennen“. Bei Fehlalarmen (Bäume, Regen, Nachtsicht) niedriger stellen."},
			{Key: "notify", Label: "Bei Ereignissen benachrichtigen", Type: "checkbox",
				Help: "Über deine Kanäle unter Einstellungen → Benachrichtigungen, höchstens alle 5 Minuten pro Kamera."},
			{Key: "still", Label: "Standbild", Type: "select", Options: []string{"beim Laden", "alle 10 Sekunden", "alle 30 Sekunden", "jede Minute", "alle 5 Minuten", "alle 15 Minuten", "nur auf Klick"}, Display: true,
				Help: "Wie oft die Kachel ein neues Bild holt – nur solange das Dashboard offen ist. „nur auf Klick“ weckt Akku-Kameras nicht."},
			{Key: "list", Label: "Ereignisliste", Type: "select", Options: []string{"3", "0", "5", "10"}, Display: true,
				Help: "Wie viele der letzten Ereignisse unter dem Bild stehen (0 = nur das letzte im Bild)."},
		},
	}
}

// CameraView is what the browser needs for a camera tile.
type CameraView struct {
	Still   string        `json:"still"`            // load | seconds | click
	Detect  string        `json:"detect,omitempty"` // onvif | stream
	Events  []CameraEvent `json:"events,omitempty"`
	List    int           `json:"list"`
	HasLive bool          `json:"hasLive"`
}

type CameraEvent struct {
	ID    int64  `json:"id"`
	At    string `json:"at"`
	Kind  string `json:"kind"`
	Image bool   `json:"image"`
}

func (c cameraTile) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	if in.V("rtsp") == "" && in.V("onvif") == "" && in.V("snapshot") == "" {
		return nil, in.Errorf("RTSP-, ONVIF- oder Standbild-Adresse eintragen")
	}
	still := map[string]string{"alle 10 Sekunden": "10", "alle 30 Sekunden": "30", "jede Minute": "60", "alle 5 Minuten": "300", "alle 15 Minuten": "900", "nur auf Klick": "click"}[in.V("still")]
	if still == "" {
		still = "load"
	}
	list, err := strconv.Atoi(in.V("list"))
	if err != nil {
		list = 3
	}
	v := &CameraView{Still: still, Detect: CameraDetect(in), List: list, HasLive: in.V("rtsp") != "" || in.V("onvif") != ""}
	for _, e := range camera.Events(in.TileID) {
		v.Events = append(v.Events, CameraEvent{ID: e.ID, At: e.At.Format(time.RFC3339), Kind: in.T(e.Kind), Image: e.Image != nil})
	}
	return &Widget{Camera: v}, nil
}

func (c cameraTile) Test(ctx context.Context, in *Instance) error {
	if in.V("rtsp") == "" && in.V("onvif") == "" && in.V("snapshot") == "" {
		return in.Errorf("RTSP-, ONVIF- oder Standbild-Adresse eintragen")
	}
	if o := CameraONVIF(in); o != nil {
		info, err := o.Info(ctx)
		if err != nil {
			return CameraError(in, err)
		}
		if in.V("rtsp") == "" && info.StreamURI == "" && in.V("snapshot") == "" && info.SnapshotURI == "" {
			return in.Errorf("Die Kamera nennt über ONVIF keinen Stream – bitte die RTSP-Adresse eintragen")
		}
	}
	if in.V("rtsp") != "" || in.V("onvif") != "" {
		src, err := CameraSource(ctx, in)
		if err != nil {
			return err
		}
		if _, err := camera.Probe(ctx, src); err != nil {
			return CameraError(in, err)
		}
		return nil
	}
	_, _, err := CameraSnapshot(ctx, in)
	return err
}

// CameraDetect says where a camera tile's events come from: "onvif",
// "stream" or "" (none).
func CameraDetect(in *Instance) string {
	switch in.V("events") {
	case "aus":
		return ""
	case "aus dem Stream erkennen":
		if in.V("rtsp") != "" || in.V("onvif") != "" {
			return "stream"
		}
		return ""
	}
	if in.V("onvif") != "" {
		return "onvif"
	}
	return ""
}

// CameraONVIF returns the tile's ONVIF client, or nil without an address.
func CameraONVIF(in *Instance) *camera.ONVIF {
	if in.V("onvif") == "" {
		return nil
	}
	return &camera.ONVIF{Address: in.V("onvif"), User: in.V("username"), Pass: in.Values["password"], Client: in.Client}
}

func (c cameraTile) onvifInfo(ctx context.Context, in *Instance) (camera.ONVIFInfo, error) {
	o := CameraONVIF(in)
	if o == nil {
		return camera.ONVIFInfo{}, nil
	}
	return remember(in, "onvif\x00"+in.V("onvif")+"\x00"+in.V("username")+"\x00"+in.Values["password"], time.Hour, func() (camera.ONVIFInfo, error) {
		return o.Info(ctx)
	})
}

// CameraSource returns where the tile's video comes from, with the login
// filled in and – for an external account – only public addresses allowed.
func CameraSource(ctx context.Context, in *Instance) (camera.Source, error) {
	addr := in.V("rtsp")
	if addr == "" {
		info, err := cameraTile{}.onvifInfo(ctx, in)
		if err != nil {
			return camera.Source{}, CameraError(in, err)
		}
		addr = info.StreamURI
	}
	if addr == "" {
		return camera.Source{}, camera.Shown(in.T("Keine RTSP-Adresse eingetragen"))
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Host == "" {
		return camera.Source{}, camera.Shown(in.T("Die RTSP-Adresse muss mit rtsp:// beginnen"))
	}
	if u.User == nil && in.V("username") != "" {
		u.User = url.UserPassword(in.V("username"), in.Values["password"])
	}
	scope := ""
	if in.PublicOnly {
		scope = "public"
	}
	return camera.Source{URL: u.String(), Scope: scope, Dial: DialerFor(in.PublicOnly)}, nil
}

// CameraSnapshot loads the camera's own JPEG, when it has a snapshot address
// (entered, or found through ONVIF). ok is false when there is none.
func CameraSnapshot(ctx context.Context, in *Instance) ([]byte, string, error) {
	addr := in.V("snapshot")
	if addr == "" {
		info, err := cameraTile{}.onvifInfo(ctx, in)
		if err == nil {
			addr = info.SnapshotURI
		}
	}
	if addr == "" {
		return nil, "", ErrNoSnapshot
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		return nil, "", camera.Shown(in.T("Die Standbild-Adresse muss mit http:// oder https:// beginnen"))
	}
	data, ct, err := camera.Snapshot(ctx, in.Client, addr, in.V("username"), in.Values["password"])
	if err != nil {
		return nil, "", CameraError(in, err)
	}
	return data, ct, nil
}

// CameraMoveLogin takes a login written into an address (rtsp://user:pw@…) out
// of it and into the tile's user and password fields: addresses are stored and
// exported as plain text, secrets encrypted and never exported.
func CameraMoveLogin(config, secrets map[string]string) bool {
	moved := false
	for _, key := range []string{"rtsp", "snapshot", "onvif"} {
		u, err := url.Parse(config[key])
		if err != nil || u.User == nil {
			continue
		}
		if name := u.User.Username(); name != "" && (key == "rtsp" || config["username"] == "") {
			config["username"] = name
		}
		if pw, ok := u.User.Password(); ok && (key == "rtsp" || secrets["password"] == "") {
			secrets["password"] = pw
		}
		u.User = nil
		config[key] = u.String()
		moved = true
	}
	return moved
}

// CameraError is a camera error as the browser may see it, in the user's
// language: without the camera's address, which viewers of a shared tile must
// not learn. The details are in the debug log.
func CameraError(in *Instance, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), ErrHomeNetwork.Error()) {
		return camera.Shown(in.T(ErrHomeNetwork.Error()))
	}
	return camera.Shown(in.T(camera.Public(err).Error()))
}

// ErrNoSnapshot: the camera has no snapshot address; take a picture from the stream.
var ErrNoSnapshot = errors.New("no snapshot address")
