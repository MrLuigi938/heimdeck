<script>
  // Login with an identity provider (OpenID Connect) – admin only, shown
  // under user management. The client secret is never sent back; an empty
  // field keeps the stored one.
  import { get, put } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t } from './i18n.svelte.js';

  let cfg = $state(null);
  let secret = $state('');
  let secretSet = $state(false);
  let callback = $state('');
  let saving = $state(false);
  let open = $state(false);

  async function load() {
    try {
      const res = await get('/sso');
      cfg = res.config;
      secretSet = res.secretSet;
      callback = res.callback;
      open = cfg.enabled;
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  load();

  async function save(e) {
    e?.preventDefault();
    saving = true;
    try {
      await put('/sso', { ...$state.snapshot(cfg), clientSecret: secret });
      secret = '';
      toast(cfg.enabled ? t('SSO gespeichert – der Anbieter ist erreichbar.') : t('Gespeichert'));
      load();
    } catch (err) {
      toast(err.message, 'bad');
    } finally {
      saving = false;
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(callback);
      toast(t('Kopiert'));
    } catch {
      toast(t('Kopieren nicht möglich – bitte von Hand markieren.'), 'bad');
    }
  }
</script>

{#if cfg}
  <details class="sso" bind:open>
    <summary>🔑 {t('Anmeldung mit SSO (OpenID Connect)')} {#if cfg.enabled}<span class="tag">{t('aktiv')}</span>{/if}</summary>
    <form onsubmit={save}>
      <p class="help">{t('Mit Authentik, Authelia, Keycloak, Pocket ID & Co.: Lege dort eine Anwendung bzw. einen OAuth2/OpenID-Provider für Heimdeck an und trag als Weiterleitungs-Adresse (Redirect URI) diese ein:')}</p>
      <div class="secret-row">
        <input readonly value={callback} onfocus={(e) => e.target.select()} aria-label={t('Weiterleitungs-Adresse')} />
        <button type="button" class="small" onclick={copy}>📋 {t('Kopieren')}</button>
      </div>
      <label class="field">
        <span>{t('Issuer-Adresse')}</span>
        <input bind:value={cfg.issuer} placeholder="https://auth.example.com/application/o/heimdeck" spellcheck="false" />
        <span class="help">{t('Steht beim Anbieter als „Issuer“ oder „OpenID Configuration Issuer“ – ohne /.well-known/… am Ende.')}</span>
      </label>
      <div class="two">
        <label class="field"><span>{t('Client-ID')}</span><input bind:value={cfg.clientId} spellcheck="false" autocomplete="off" /></label>
        <label class="field"><span>{t('Client-Secret')}</span><input type="password" bind:value={secret} placeholder={secretSet ? t('gespeichert – leer lassen zum Behalten') : ''} autocomplete="new-password" /></label>
      </div>
      <label class="field"><span>{t('Name auf dem Knopf')}</span><input bind:value={cfg.label} placeholder="Authentik" /></label>
      <label class="check"><input type="checkbox" bind:checked={cfg.autoCreate} /> {t('Neue Konten beim ersten Anmelden automatisch anlegen')}</label>
      <div class="two">
        <label class="field">
          <span>{t('Nur diese Gruppe darf rein (optional)')}</span>
          <input bind:value={cfg.allowedGroup} placeholder="heimdeck" spellcheck="false" />
        </label>
        <label class="field">
          <span>{t('Admin-Gruppe (optional)')}</span>
          <input bind:value={cfg.adminGroup} placeholder="heimdeck-admins" spellcheck="false" />
        </label>
      </div>
      <span class="help">{t('Mit Admin-Gruppe bestimmt der Anbieter bei jeder Anmeldung, wer Admin ist – nur der Hauptadmin bleibt immer Admin. Ohne Admin-Gruppe werden neue Konten normale Benutzer.')}</span>
      <label class="check"><input type="checkbox" bind:checked={cfg.passwordOff} /> {t('Anmeldung nur noch über SSO')}</label>
      <span class="help">{t('Dann meldet sich nur noch der Hauptadmin auch mit Passwort an – als Notzugang, falls der Anbieter einmal nicht erreichbar ist. Bestehende Konten verknüpfen sich vorher unter Konto und Sicherung mit SSO.')}</span>
      <details class="more">
        <summary>{t('Weitere Optionen')}</summary>
        <label class="field">
          <span>{t('Scopes')}</span>
          <input bind:value={cfg.scopes} spellcheck="false" />
          <span class="help">{t('Meldet der Anbieter „invalid_scope“, „groups“ weglassen.')}</span>
        </label>
        <label class="field">
          <span>{t('Adresse von Heimdeck (optional)')}</span>
          <input bind:value={cfg.publicUrl} placeholder="https://heimdeck.example.com" spellcheck="false" />
          <span class="help">{t('Nur nötig, wenn die Weiterleitungs-Adresse oben nicht stimmt, z. B. hinter einem Reverse Proxy.')}</span>
        </label>
        <label class="check"><input type="checkbox" bind:checked={cfg.insecure} /> {t('Zertifikat des Anbieters nicht prüfen (selbstsigniert)')}</label>
      </details>
      <label class="check"><input type="checkbox" bind:checked={cfg.enabled} /> <b>{t('Anmeldung mit SSO einschalten')}</b></label>
      <div class="row"><span class="spacer"></span><button class="primary small" disabled={saving}>{saving ? t('Prüfe …') : t('Speichern')}</button></div>
    </form>
  </details>
{/if}

<style>
  .sso { margin-top: 18px; border: 1px solid var(--border); border-radius: var(--r-md); padding: 10px 12px; }
  .sso > summary { cursor: pointer; font-weight: 600; display: flex; gap: 8px; align-items: center; }
  form { display: grid; gap: 12px; margin-top: 12px; }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  @media (max-width: 600px) { .two { grid-template-columns: 1fr; } }
  .more summary { cursor: pointer; color: var(--muted); font-size: 13px; margin-bottom: 8px; }
  .more { display: grid; gap: 10px; }
  .secret-row { display: flex; gap: 8px; }
  .secret-row input { flex: 1; min-width: 0; }
  .row { display: flex; align-items: center; }
  .spacer { flex: 1; }
  .tag { font-size: 11px; font-weight: 500; padding: 1px 7px; border-radius: 999px; border: 1px solid color-mix(in srgb, var(--ok) 45%, transparent); color: var(--ok); }
</style>
