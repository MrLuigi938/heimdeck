package server

import "log/slog"

// Settings are personal (look, search, shortcuts, notifications) or belong
// to the whole instance and can only be changed by an admin. Personal
// settings are stored per user; the instance's "language" stays as well, for
// the login page and as the start value of new accounts.
var instanceSettings = map[string]bool{
	"localHosts": true, "dockerControl": true, "dockerUpdates": true, "debug": true, "updateCheck": true, "monitorExternal": true,
}

// settingsMoved marks that the personal settings were moved to the admin (1.4).
const settingsMoved = "_settingsPerUser"

// settingsFor returns the instance settings with the user's own on top.
func (s *Server) settingsFor(user int64) map[string]string {
	out, _ := s.store.Settings()
	if out == nil {
		out = map[string]string{}
	}
	own, _ := s.store.UserSettings(user)
	for k, v := range own {
		out[k] = v
	}
	return out
}

// saveSettings stores instance settings for the instance and the rest for
// the user. The admin's language is also the one of the login page.
func (s *Server) saveSettings(user int64, admin bool, m map[string]string) error {
	inst, own := map[string]string{}, map[string]string{}
	for k, v := range m {
		if instanceSettings[k] {
			inst[k] = v
		} else {
			own[k] = v
		}
	}
	if lang, ok := own["language"]; ok && admin {
		inst["language"] = lang
	}
	if len(inst) > 0 {
		if err := s.store.SaveSettings(inst); err != nil {
			return err
		}
	}
	if len(own) > 0 {
		return s.store.SaveUserSettings(user, own)
	}
	return nil
}

// migrateSettings moves the personal settings of an instance from before 1.4
// to its admin, once.
func (s *Server) migrateSettings() {
	settings, err := s.store.Settings()
	if err != nil || settings[settingsMoved] != "" {
		return
	}
	if name, err := s.store.FirstUsername(); err == nil {
		id, _, err := s.store.UserByName(name)
		if err != nil {
			return
		}
		keys := []string{notifySecretsKey}
		for k := range allowedSettings {
			if !instanceSettings[k] && k != "language" {
				keys = append(keys, k)
			}
		}
		if err := s.store.MoveSettings(id, keys); err != nil {
			slog.Error("Einstellungen übernehmen", "fehler", err)
			return
		}
		if lang := settings["language"]; lang != "" {
			logErr("Sprache übernehmen", s.store.SaveUserSettings(id, map[string]string{"language": lang}))
		}
		slog.Info("Einstellungen dem Admin-Konto zugeordnet", "benutzer", name)
	}
	logErr("Einstellungen übernehmen", s.store.SaveSettings(map[string]string{settingsMoved: "1"}))
}

// starterSettings gives a new account the language of whoever created it.
func (s *Server) starterSettings(user int64, lang string) {
	logErr("Sprache speichern", s.store.SaveUserSettings(user, map[string]string{"language": lang}))
}

// newAccount sets up a fresh account: its first dashboard with starter groups.
func (s *Server) newAccount(user int64, lang string) {
	s.starterSettings(user, lang)
	board, err := s.firstBoard(user)
	if err != nil {
		logErr("Dashboard anlegen", err)
		return
	}
	s.seed(lang, board)
}
