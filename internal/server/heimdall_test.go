package server

import "testing"

func TestHeimdallTiles(t *testing.T) {
	export := `[
  {"title": "Sonarr", "colour": "#161b1f", "url": "http://192.168.1.10:8989",
   "description": "{\"enabled\":true,\"override_url\":\"http://sonarr:8989\",\"apikey\":\"abc123\"}",
   "appid": "6e2a525ceb737f9ed8badcef5eb20d1502e39656", "appdescription": null, "pinned": 1, "pinned_order": 2, "tags": ["Medien"]},
  {"title": "Nextcloud", "url": "https://cloud.example.com",
   "description": "{\"enabled\":true,\"username\":\"max\",\"password\":\"geheim\"}",
   "appid": "f2baa52d02ca888455ce47823f47bf372d5eecb3", "pinned": 1, "pinned_order": 1, "tags": []},
  {"title": "Mein Router", "url": "http://192.168.1.1", "description": null, "appid": "null", "pinned": 0, "pinned_order": 3},
  {"title": "", "url": "http://leer"}
]`
	if !isHeimdallExport([]byte(export)) || isHeimdallExport([]byte(`{"version":1}`)) {
		t.Fatal("detection")
	}
	groups, order, err := heimdallTiles([]byte(export), "Heimdall")
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "Heimdall" || order[1] != "Medien" {
		t.Fatalf("order %v", order)
	}
	nc, router := groups["Heimdall"][0], groups["Heimdall"][1]
	if nc.Integration != "nextcloud" || nc.Config["username"] != "max" || nc.Secrets["password"] != "geheim" || !nc.ShowWidget {
		t.Fatalf("nextcloud %+v", nc)
	}
	if router.Integration != "link" || router.Icon != "mein-router" {
		t.Fatalf("router %+v", router)
	}
	so := groups["Medien"][0]
	if so.Integration != "sonarr" || so.Secrets["apiKey"] != "abc123" || so.APIURL != "http://sonarr:8989" {
		t.Fatalf("sonarr %+v", so)
	}
}
