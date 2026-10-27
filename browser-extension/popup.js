const ESSENTIAL = ['hatch_sess', 'hatch_vml', 'hatch_native_auth_device'];
const SITE = 'https://muse.ai';

const state = {
  cookies: [],
  format: 'header',
  loading: true,
  error: '',
};

const el = {
  label: document.getElementById('label'),
  refresh: document.getElementById('refresh'),
  status: document.getElementById('status'),
  output: document.getElementById('output'),
  copy: document.getElementById('copy'),
  formats: [...document.querySelectorAll('.format')],
};

function cookieHeader() {
  return state.cookies.map(cookie => `${cookie.name}=${cookie.value}`).join('; ');
}

function safeLabel() {
  const value = (el.label.value.trim() || 'Muse 账号').replace(/[|=\r\n]/g, '-').slice(0, 80);
  return value || 'Muse 账号';
}

function formatValue() {
  if (state.format === 'json') {
    return JSON.stringify(Object.fromEntries(state.cookies.map(cookie => [cookie.name, cookie.value])), null, 2);
  }
  if (state.format === 'batch') {
    return `${safeLabel()}|${cookieHeader()}`;
  }
  return cookieHeader();
}

function setStatus(kind, text) {
  el.status.className = `status ${kind}`;
  el.status.textContent = text;
}

function renderStatus() {
  if (state.loading) {
    setStatus('', '正在读取 muse.ai Cookie…');
    return;
  }
  if (state.error) {
    setStatus('error', state.error);
    return;
  }
  if (state.cookies.length === 0) {
    setStatus('warn', '没有找到 muse.ai Cookie。请先登录 Muse，再点“重新读取”。');
    return;
  }
  const names = new Set(state.cookies.map(cookie => cookie.name));
  const missing = ESSENTIAL.filter(name => !names.has(name));
  if (missing.length > 0) {
    setStatus('warn', `${state.cookies.length} 条 Cookie，缺少核心项：${missing.join('、')}`);
    return;
  }
  setStatus('ok', `${state.cookies.length} 条 Cookie，核心凭据完整。`);
}

function renderOutput() {
  el.output.value = state.loading || state.error || state.cookies.length === 0 ? '' : formatValue();
  el.copy.disabled = !el.output.value;
  renderStatus();
}

function render() {
  renderOutput();
  el.formats.forEach(button => {
    const active = button.dataset.format === state.format;
    button.classList.toggle('active', active);
    button.setAttribute('aria-selected', String(active));
  });
}

function getCookies(query) {
  return new Promise((resolve, reject) => {
    chrome.cookies.getAll(query, cookies => {
      const error = chrome.runtime.lastError;
      if (error) reject(new Error(error.message));
      else resolve(cookies || []);
    });
  });
}

async function load() {
  state.loading = true;
  state.error = '';
  render();

  if (!globalThis.chrome?.cookies) {
    state.loading = false;
    state.error = '请在 Chrome / Edge 扩展环境中打开此页面。';
    render();
    return;
  }

  try {
    const [all, www, root] = await Promise.all([
      getCookies({ domain: 'muse.ai' }),
      getCookies({ url: 'https://www.muse.ai/' }).catch(() => []),
      getCookies({ url: `${SITE}/` }),
    ]);
    const byName = new Map();
    // Later entries win: root URL cookies are the ones the site actually receives.
    for (const list of [all, www, root]) {
      for (const cookie of list) byName.set(cookie.name, cookie);
    }
    const rank = new Map(ESSENTIAL.map((name, index) => [name, index]));
    state.cookies = [...byName.values()].sort((a, b) => {
      const left = rank.has(a.name) ? rank.get(a.name) : ESSENTIAL.length;
      const right = rank.has(b.name) ? rank.get(b.name) : ESSENTIAL.length;
      return left - right || a.name.localeCompare(b.name);
    });
  } catch (error) {
    state.cookies = [];
    state.error = `读取 Cookie 失败：${error instanceof Error ? error.message : String(error)}`;
  }

  state.loading = false;
  render();
}

async function copyOutput() {
  const text = el.output.value;
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    el.output.focus();
    el.output.select();
    document.execCommand('copy');
  }
  const original = el.copy.textContent;
  el.copy.textContent = '已复制';
  setTimeout(() => { el.copy.textContent = original; }, 1200);
}

el.refresh.addEventListener('click', load);
el.copy.addEventListener('click', copyOutput);
el.label.addEventListener('input', () => {
  if (state.format === 'batch') renderOutput();
});
el.formats.forEach(button => {
  button.addEventListener('click', () => {
    state.format = button.dataset.format;
    render();
  });
});

load();
