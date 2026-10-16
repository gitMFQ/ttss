(() => {
  const $ = (id) => document.getElementById(id);
  const state = { authRequired: false, token: '', voices: [], history: [], db: null, previewURL: '' };
  const defaultText = '君不见黄河之水天上来，奔流到海不复回。';
  const omniLocales = ['zh-CN','zh-HK','zh-TW','en-US','en-GB','ja-JP','ko-KR','fr-FR','de-DE','es-ES','it-IT','pt-BR','ru-RU'];
  const localeNames = {'zh-CN':'中文（普通话）','zh-HK':'中文（香港）','zh-TW':'中文（台湾）','en-US':'英语（美国）','en-GB':'英语（英国）','ja-JP':'日语（日本）','ko-KR':'韩语（韩国）','fr-FR':'法语（法国）','de-DE':'德语（德国）','es-ES':'西班牙语（西班牙）','it-IT':'意大利语（意大利）','pt-BR':'葡萄牙语（巴西）','ru-RU':'俄语（俄罗斯）','en-AU':'英语（澳大利亚）','en-CA':'英语（加拿大）','fr-CA':'法语（加拿大）','zh-CN-liaoning':'中文（辽宁）','zh-CN-shaanxi':'中文（陕西）'};
  const friendlyVoices = {'zh-CN-XiaoxiaoNeural':'晓晓','zh-CN-XiaoyiNeural':'晓伊','zh-CN-YunjianNeural':'云健','zh-CN-YunxiNeural':'云希','zh-CN-YunxiaNeural':'云夏','zh-CN-YunyangNeural':'云扬','zh-HK-HiuGaaiNeural':'曉佳','zh-HK-HiuMaanNeural':'曉曼','zh-HK-WanLungNeural':'雲龍','zh-TW-HsiaoChenNeural':'曉臻','zh-TW-YunJheNeural':'雲哲','zh-TW-HsiaoYuNeural':'曉雨'};
  const styleNames = {general:'默认',cheerful:'愉快',excited:'兴奋',hopeful:'充满希望',calm:'平静',gentle:'温柔',affectionate:'亲昵',chat:'聊天',serious:'严肃',lyrical:'抒情',sad:'悲伤',angry:'愤怒',fearful:'恐惧',terrified:'惊恐',surprised:'惊讶',disgruntled:'不满',unfriendly:'冷淡',whispering:'耳语','narration-relaxed':'轻松旁白','customer-service':'客服','voice-assistant':'语音助手',comforting:'安慰'};
  const localeDisplayNames = (() => { try { return new Intl.DisplayNames(['zh-CN'], { type:'language' }); } catch { return null; } })();

  function showToast(message, isError = false) {
    const toast = $('toast'); toast.textContent = message; toast.classList.toggle('error-toast', isError); toast.classList.remove('hidden');
    clearTimeout(showToast.timer); showToast.timer = setTimeout(() => toast.classList.add('hidden'), 2600);
  }
  function setLoginView(show) { $('login-view').classList.toggle('hidden', !show); $('app-view').classList.toggle('hidden', show); }
  function authHeaders() { return state.token ? { Authorization: `Bearer ${state.token}` } : {}; }
  function friendlyLocale(value) { return localeNames[value] || localeDisplayNames?.of(value) || value; }
  function voiceShortName(value) { const match = value.match(/\(([^,()]+),\s*([^()]+)\)/); return match ? `${match[1].trim()}-${match[2].trim()}` : value; }
  function friendlyVoice(voice) { const short = voiceShortName(voice.value); return friendlyVoices[short] || voice.label || short; }
  function selectedVoice() { return state.voices.find(v => v.value === $('voice').value); }
  function currentService() { return $('service').value || 'edge'; }

  async function init() {
    $('text').value ||= defaultText;
    $('text').addEventListener('input', updateCharCount);
    $('service').addEventListener('change', () => {
      $('voice-filter').value = ''; $('omni').checked = false; $('personality').value = ''; $('role').value = 'default'; $('lang-skill').value = '';
      loadVoices(true);
    });
    $('locale').addEventListener('change', () => populateVoices(false));
    $('voice-filter').addEventListener('input', () => populateVoices(true));
    $('voice').addEventListener('change', () => { populatePersonality(); refreshAdvanced(); });
    $('safe-mode').addEventListener('change', () => { populatePersonality(); refreshAdvanced(); });
    $('personality').addEventListener('change', refreshAdvanced);
    $('omni').addEventListener('change', refreshAdvanced);
    $('style-degree').addEventListener('input', () => $('degree-value').textContent = Number($('style-degree').value).toFixed(1));
    for (const name of ['rate','pitch','volume']) $(name).addEventListener('input', () => $(`${name}-value`).textContent = $(name).value);
    $('convert-button').addEventListener('click', generateSpeech);
    $('legado-button').addEventListener('click', openLegado);
    $('qr-button').addEventListener('click', openQR);
    $('close-qr').addEventListener('click', () => $('qr-dialog').close());
    $('copy-url').addEventListener('click', async () => { try { await navigator.clipboard.writeText($('qr-url').value); showToast('配置链接已复制'); } catch { $('qr-url').select(); document.execCommand('copy'); showToast('配置链接已复制'); } });
    $('logout-button').addEventListener('click', logout);
    $('login-form').addEventListener('submit', login);
    $('service').value = 'edge';
    updateCharCount();
    openHistory().catch(() => {});
    const response = await fetch('/api/config');
    const config = await response.json();
    state.authRequired = Boolean(config.authRequired);
    state.token = localStorage.getItem('auth_token') || '';
    if (state.authRequired && !state.token) { setLoginView(true); return; }
    enterApp();
  }

  async function login(event) {
    event.preventDefault();
    const token = $('login-token').value;
    $('login-error').classList.add('hidden');
    try {
      const response = await fetch('/api/login', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({token}) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || '登录失败，请检查令牌');
      localStorage.setItem('auth_token', result.token); state.token = result.token; enterApp();
    } catch (error) { $('login-error').textContent = error.message; $('login-error').classList.remove('hidden'); }
  }

  function logout() { localStorage.removeItem('auth_token'); state.token = ''; setLoginView(true); $('login-token').value = ''; }
  function enterApp() {
    setLoginView(false); $('logout-button').classList.toggle('hidden', !state.authRequired); loadVoices(false);
  }

  async function loadVoices(reset) {
    const service = currentService();
    $('service-hint').textContent = service === 'ms' ? '免费 · 支持风格、角色与多语言音色' : '免费 · 稳定自然的 Neural 音色';
    $('voice').innerHTML = '<option value="">正在加载音色…</option>';
    $('locale').innerHTML = '<option value="">正在加载地区…</option>';
    $('voice-count').textContent = '';
    try {
      const response = await fetch(`/api/voices?service=${encodeURIComponent(service)}`);
      const voices = await response.json();
      if (!response.ok) throw new Error(voices.error || '加载音色失败');
      state.voices = Array.isArray(voices) ? voices : [];
      const locales = [...new Set(state.voices.map(v => v.locale).filter(Boolean))].sort((a,b) => a.localeCompare(b));
      $('locale').innerHTML = '<option value="">全部地区</option>' + locales.map(locale => `<option value="${escapeHTML(locale)}">${escapeHTML(friendlyLocale(locale))} · ${escapeHTML(locale)}</option>`).join('');
      const initial = !reset && locales.includes('zh-CN') ? 'zh-CN' : (locales.includes('zh-CN') ? 'zh-CN' : (locales[0] || ''));
      $('locale').value = initial;
      populateVoices();
      $('voice-count').textContent = `${state.voices.length.toLocaleString()} 个音色`;
    } catch (error) {
      $('locale').innerHTML = '<option value="">加载失败</option>'; $('voice').innerHTML = '<option value="">加载音色失败</option>'; showToast(error.message, true);
    }
  }

  function populateVoices(keepSelection = false) {
    const locale = $('locale').value; const previous = $('voice').value; const search = $('voice-filter').value.trim().toLowerCase();
    const visible = state.voices.filter(voice => (!locale || voice.locale === locale) && (!search || `${voice.label} ${voice.value} ${voice.locale}`.toLowerCase().includes(search)));
    $('voice').innerHTML = visible.map(voice => `<option value="${escapeHTML(voice.value)}">${escapeHTML(friendlyVoice(voice))} · ${escapeHTML(voice.gender || '')}</option>`).join('') || '<option value="">未找到音色</option>';
    if (visible.length) {
      const preferred = 'zh-CN-XiaoxiaoNeural';
      const defaultVoice = (keepSelection && visible.find(v => v.value === previous)) || visible.find(v => voiceShortName(v.value) === preferred) || visible[0];
      $('voice').value = defaultVoice.value;
    }
    populatePersonality(); refreshAdvanced();
  }

  function populatePersonality() {
    const voice = selectedVoice(); const service = currentService(); const safe = $('safe-mode').checked;
    const choices = [];
    if (service === 'ms' && !safe) choices.push(['auto','自动风格（按规则/情绪桥接）']);
    for (const style of (voice?.personalities || [])) choices.push([style, styleNames[style.toLowerCase()] || style]);
    $('personality').innerHTML = '<option value="">默认</option>' + choices.map(([value,label]) => `<option value="${escapeHTML(value)}">${escapeHTML(label)}</option>`).join('');
  }

  function refreshAdvanced() {
    const ms = currentService() === 'ms'; const safe = $('safe-mode').checked; const voice = selectedVoice();
    if (ms && !voice?.omniCapable) $('omni').checked = false;
    const omni = $('omni').checked;
    const previousLanguage = $('lang-skill').value; const previousRole = $('role').value;
    $('ms-options').classList.toggle('hidden', !ms);
    $('omni').disabled = !voice?.omniCapable; $('omni-label').classList.toggle('disabled', !voice?.omniCapable);
    $('format').disabled = safe; $('format-hint').classList.toggle('hidden', !safe);
    $('personality').closest('.field').classList.toggle('hidden', ms && (safe || omni));
    const languageChoices = omni ? [...new Set([voice?.locale, ...omniLocales].filter(Boolean))] : [...new Set([voice?.locale, ...(voice?.secondaryLocales || [])].filter(Boolean))];
    $('language-field').classList.toggle('hidden', !ms || languageChoices.length <= 1);
    $('lang-skill').innerHTML = '<option value="">默认音色语言</option>' + languageChoices.map(value => `<option value="${escapeHTML(value)}">${escapeHTML(friendlyLocale(value))} (${escapeHTML(value)})</option>`).join('');
    const language = languageChoices.includes(previousLanguage) ? previousLanguage : (languageChoices[0] || '');
    $('lang-skill').value = language;
    const roles = voice?.roles || [];
    $('role-field').classList.toggle('hidden', !ms || safe || omni || !roles.length);
    $('role').innerHTML = '<option value="default">默认角色</option>' + roles.map(role => `<option value="${escapeHTML(role)}">${escapeHTML(role)}</option>`).join('');
    $('role').value = roles.includes(previousRole) ? previousRole : 'default';
    $('degree-field').classList.toggle('hidden', !ms || safe || omni);
    $('rules-field').classList.toggle('hidden', !ms || safe || omni || $('personality').value !== 'auto');
  }

  function optionsObject() {
    const service = currentService();
    const options = { voice:$('voice').value, rate:Number($('rate').value), pitch:Number($('pitch').value), volume:Number($('volume').value) };
    const personality = $('personality').value;
    if (personality) options.personality = personality;
    if (service === 'ms') Object.assign(options, {
      safeMode:$('safe-mode').checked, omni:$('omni').checked, region:$('region').value, format:$('format').value,
      styleDegree:Number($('style-degree').value), role:$('role').value === 'default' ? '' : $('role').value,
      langSkill:$('lang-skill').value, autoStyleRules:$('style-rules').value,
    });
    return options;
  }

  async function generateSpeech() {
    const text = $('text').value; const options = optionsObject(); $('form-error').classList.add('hidden');
    if (!text.trim()) { showFormError('请输入要转换的文本。'); return; }
    if (!options.voice) { showFormError('请先选择一个发音人。'); return; }
    const button = $('convert-button'); button.disabled = true; button.innerHTML = '<span class="spinner"></span><span>正在生成…</span>';
    try {
      const response = await fetch('/api/text-to-speech', { method:'POST', headers:{'Content-Type':'application/json', ...authHeaders()}, body:JSON.stringify({text, service:currentService(), options}) });
      if (!response.ok) {
        if (response.status === 401 && state.authRequired) { logout(); $('login-error').textContent = '登录令牌无效，请重新登录。'; $('login-error').classList.remove('hidden'); throw new Error('登录令牌无效，请重新登录。'); }
        const message = await response.json().catch(() => ({})); throw new Error(message.error || `生成失败（${response.status}）`);
      }
      const blob = await response.blob(); const dataURI = await blobToDataURL(blob); const uri = URL.createObjectURL(blob);
      if (state.previewURL) URL.revokeObjectURL(state.previewURL);
      state.previewURL = uri; $('preview-audio').src = uri; $('download-audio').href = uri; $('download-audio').download = `speech-${Date.now()}.${extensionFor(blob.type)}`; $('latest-audio').classList.remove('hidden');
      const record = {id:crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`, uri:dataURI, text, service:currentService(), options, createAt:Date.now()};
      await saveRecord(record); $('preview-audio').play().catch(() => {}); showToast('语音生成完成');
    } catch (error) { showFormError(error.message || '生成语音失败'); }
    finally { button.disabled = false; button.innerHTML = '<span>生成语音</span><span class="arrow">→</span>'; }
  }

  function showFormError(message) { $('form-error').textContent = message; $('form-error').classList.remove('hidden'); }
  function updateCharCount() { $('char-count').textContent = `${[...$('text').value].length.toLocaleString()} 字`; }
  function blobToDataURL(blob) { return new Promise((resolve,reject) => { const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.onerror = reject; reader.readAsDataURL(blob); }); }
  function extensionFor(type) { if (type.includes('wav')) return 'wav'; if (type.includes('ogg')) return 'ogg'; if (type.includes('webm')) return 'webm'; return 'mp3'; }

  function getImportURL() {
    const params = new URLSearchParams(); const options = optionsObject();
    for (const [key,value] of Object.entries(options)) if (value !== '' && value != null) params.set(key, String(value));
    params.set('service', currentService()); params.set('protocol', location.protocol.replace(':',''));
    if (state.token) params.set('token', state.token);
    return `${location.origin}/api/legado-import?${params.toString()}`;
  }
  function openLegado() { if (!optionsObject().voice) { showToast('请先选择发音人', true); return; } location.href = `legado://import/httpTTS?src=${encodeURIComponent(getImportURL())}`; }
  async function openQR() {
    if (!optionsObject().voice) { showToast('请先选择发音人', true); return; }
    const url = getImportURL(); $('qr-url').value = url; $('qr-image').src = `/api/qrcode?url=${encodeURIComponent(url)}`; $('qr-dialog').showModal();
  }

  function openHistory() {
    return new Promise((resolve,reject) => {
      const request = indexedDB.open('tts-history-db',1);
      request.onupgradeneeded = () => { const db = request.result; if (!db.objectStoreNames.contains('history')) db.createObjectStore('history',{keyPath:'id'}); };
      request.onerror = () => reject(request.error);
      request.onsuccess = () => { state.db = request.result; refreshHistory().then(resolve).catch(reject); };
    });
  }
  function refreshHistory() {
    return new Promise((resolve,reject) => {
      if (!state.db) return resolve();
      const request = state.db.transaction('history','readonly').objectStore('history').getAll();
      request.onsuccess = () => { state.history = request.result.sort((a,b) => b.createAt-a.createAt); renderHistory(); resolve(); };
      request.onerror = () => reject(request.error);
    });
  }
  function saveRecord(record) {
    return new Promise((resolve,reject) => {
      if (!state.db) return resolve();
      const request = state.db.transaction('history','readwrite').objectStore('history').put(record);
      request.onsuccess = () => refreshHistory().then(resolve).catch(reject); request.onerror = () => reject(request.error);
    });
  }
  function deleteRecord(id) {
    const request = state.db.transaction('history','readwrite').objectStore('history').delete(id);
    request.onsuccess = () => { refreshHistory(); showToast('已删除历史记录'); };
  }
  function renderHistory() {
    $('history-count').textContent = state.history.length;
    if (!state.history.length) { $('history-list').innerHTML = '<div class="empty-state"><div class="empty-icon">◷</div><b>还没有生成记录</b><span>生成的语音会保存在此处</span></div>'; return; }
    $('history-list').innerHTML = state.history.map(record => `<article class="history-item"><p class="history-text">${escapeHTML(record.text)}</p><audio controls preload="none" src="${escapeHTML(record.uri)}"></audio><div class="history-meta"><span>${record.service === 'ms' ? '微软翻译' : 'Edge'} · ${escapeHTML(record.options?.voice || '')}</span><span>${new Date(record.createAt).toLocaleString('zh-CN',{hour12:false})}</span><button class="delete-record" data-delete="${escapeHTML(record.id)}">删除</button></div></article>`).join('');
    $('history-list').querySelectorAll('[data-delete]').forEach(button => button.addEventListener('click', () => deleteRecord(button.dataset.delete)));
  }
  function escapeHTML(value) { return String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char])); }

  init().catch(error => showToast(error.message || '页面初始化失败', true));
})();
