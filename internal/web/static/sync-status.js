(() => {
  const root = document.getElementById('sync-status');
  if (!root) return;
  const summary = root.querySelector('summary');
  const retry = document.getElementById('retry-import');
  const initialCompleted = Number(root.dataset.completed);
  let requestVersion = 0;

  function closePanel(restoreFocus = false) {
    root.open = false;
    if (restoreFocus) summary.focus();
  }

  document.getElementById('sync-close').addEventListener('click', () => closePanel(true));
  document.addEventListener('click', event => {
    if (!root.contains(event.target)) closePanel();
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && root.open) closePanel(true);
  });
  root.addEventListener('focusout', event => {
    if (event.relatedTarget && !root.contains(event.relatedTarget)) closePanel();
  });

  function updateTime(unix) {
    document.getElementById('sync-next').hidden = !unix;
    document.querySelector('#sync-next time').textContent = unix
      ? new Date(unix * 1000).toLocaleString() : '';
  }
  updateTime(Number(document.querySelector('#sync-next time').dataset.unix));

  function render(status) {
    const newActivities = Math.max(0, status.completed - initialCompleted);
    root.dataset.state = status.state;
    document.getElementById('sync-label').textContent = status.state === 'complete' && newActivities > 0
      ? `${newActivities} new ${newActivities === 1 ? 'activity' : 'activities'}` : status.label;
    summary.setAttribute('aria-label', 'Activity import status: ' + document.getElementById('sync-label').textContent);
    document.getElementById('sync-message').textContent = status.message;
    document.getElementById('sync-counts').textContent = `${status.completed} imported and analyzed · ${status.pending} waiting`
      + (status.failed ? ` · ${status.failed} import failures` : '')
      + (status.map_failed ? ` · ${status.map_failed} map details unavailable` : '')
      + (status.map_blocked ? ` · ${status.map_blocked} map details blocked` : '')
      + (status.publishing_failed ? ` · ${status.publishing_failed} publishing failures` : '')
      + (status.publishing_blocked ? ` · ${status.publishing_blocked} publishing blocked` : '');
    document.getElementById('sync-new-activities').hidden = newActivities === 0;
    document.getElementById('sync-discovering').hidden = !status.discovering;
    document.getElementById('sync-reconnect').hidden = !(status.blocked || status.publishing_blocked || status.map_blocked);
    retry.hidden = !(status.failed || status.map_failed || status.publishing_failed);
    updateTime(status.next_run_at);
    document.getElementById('sync-errors').replaceChildren(...status.errors.map(error => {
      const p = document.createElement('p');
      if (error.count > 1) {
        const count = document.createElement('strong');
        count.textContent = `${error.count} attempts: `;
        p.append(count);
      }
      p.append(document.createTextNode(error.message + ' '));
      const reference = document.createElement('small');
      reference.textContent = `Reference #${error.job_id}`;
      p.append(reference);
      return p;
    }));
  }

  async function loadStatus(method = 'GET', url = '/api/sync') {
    const version = ++requestVersion;
    const response = await fetch(url, { method, cache: 'no-store' });
    if (!response.ok) throw new Error('Could not load import status');
    const status = await response.json();
    if (version === requestVersion) render(status);
  }

  const sync = document.getElementById('sync-strava');
  sync.addEventListener('click', async () => {
    sync.disabled = true;
    sync.textContent = 'Requesting sync…';
    try {
      await loadStatus('POST', '/api/sync/latest');
      sync.textContent = 'Sync with Strava';
    } catch {
      sync.textContent = 'Could not request sync. Try again.';
    } finally { sync.disabled = false; }
  });

  retry.addEventListener('click', async () => {
    retry.disabled = true;
    retry.textContent = 'Retrying…';
    try {
      await loadStatus('POST');
      retry.textContent = 'Retry failed work';
    } catch {
      retry.textContent = 'Could not retry. Try again.';
    } finally {
      retry.disabled = false;
    }
  });
  setInterval(() => {
    if (!document.hidden && !retry.disabled) loadStatus().catch(() => {});
  }, 15000);
})();
