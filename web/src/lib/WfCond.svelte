<script>
  // One condition: a value of a tile, or whether a service answers.
  import { t } from './i18n.svelte.js';

  let { cond = $bindable(), tiles = [] } = $props();
  const tileById = (id) => tiles.find((x) => x.id === Number(id));
  let choices = $derived(tiles.filter((x) => (cond.kind === 'value' ? x.stats.length : x.monitored)));
</script>

<div class="wf-fields">
  <div class="wf-row">
    <select bind:value={cond.kind} aria-label={t('Bedingung')}>
      <option value="value">📈 {t('ein Wert einer Kachel')}</option>
      <option value="online">🟢 {t('ein Dienst ist erreichbar')}</option>
      <option value="offline">🔴 {t('ein Dienst ist nicht erreichbar')}</option>
    </select>
    <select bind:value={cond.tile} onchange={() => (cond.stat = '')} aria-label={t('Kachel')}>
      <option value={0}>{t('Kachel wählen …')}</option>
      {#each choices as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
    </select>
  </div>
  {#if cond.kind === 'value'}
    <div class="wf-row">
      <select bind:value={cond.stat} disabled={!cond.tile} aria-label={t('Wert')}>
        <option value="">{t('Wert wählen …')}</option>
        {#each tileById(cond.tile)?.stats || [] as st (st.label)}<option value={st.label}>{st.label} ({st.value})</option>{/each}
      </select>
      <select bind:value={cond.op} class="wf-op" aria-label={t('Vergleich')}>
        <option value=">">{t('über')}</option>
        <option value="<">{t('unter')}</option>
        <option value="=">{t('gleich')}</option>
      </select>
      <input bind:value={cond.value} placeholder="0" class="wf-num" aria-label={t('Grenze')} />
    </div>
  {/if}
</div>
