// Push-to-talk client: hold the button (or the space bar), speak, release.
// The recording is posted to /v1/turn and the spoken reply is played back.
'use strict';

(() => {
  // Presses shorter than this are treated as accidental taps.
  const MIN_PRESS_MS = 300;
  // Recording stops by itself after this long, well under the upload limit.
  const MAX_RECORD_MS = 60000;
  // The microphone is released after this much idle time, so the browser's
  // recording indicator does not stay on.
  const RELEASE_MIC_AFTER_MS = 20000;
  // Preferred recording formats, best first. Safari offers only MP4.
  const MIME_CANDIDATES = [
    'audio/webm;codecs=opus',
    'audio/ogg;codecs=opus',
    'audio/webm',
    'audio/mp4;codecs=mp4a.40.2',
    'audio/mp4',
  ];
  const MAX_TURNS = 30;

  const READY = 'Listo para escuchar';
  const STAGE_NAMES = {
    read: 'lectura del audio',
    decode: 'decodificación',
    transcribe: 'transcripción',
    chat: 'modelo de lenguaje',
    synthesize: 'síntesis de voz',
  };
  const TIMINGS = [
    ['Decode', 'audio'],
    ['Transcribe', 'transcripción'],
    ['Chat', 'respuesta'],
    ['Synthesize', 'voz'],
    ['Total', 'total'],
  ];

  const app = document.querySelector('.app');
  const talk = document.getElementById('talk');
  const statusLine = document.getElementById('status');
  const notice = document.getElementById('notice');
  const log = document.getElementById('log');
  const empty = document.getElementById('empty');
  const player = document.getElementById('player');

  let state = 'idle';
  let stream = null;
  let recorder = null;
  let recordStart = 0;
  let pressed = false;
  let releaseTimer = 0;
  let maxTimer = 0;
  let replyURL = '';
  let silentURL = '';

  function setState(next, text) {
    state = next;
    app.dataset.state = next;
    statusLine.textContent = text;
    talk.disabled = next === 'processing' || next === 'unavailable';
    talk.setAttribute('aria-pressed', next === 'recording' ? 'true' : 'false');
  }

  function showNotice(message) {
    notice.textContent = message;
    notice.hidden = !message;
  }

  function fail(message, status = 'No se pudo completar') {
    showNotice(message);
    setState('error', status);
  }

  function unavailableReason() {
    if (!window.isSecureContext) {
      return 'El micrófono sólo funciona en una conexión segura. Abrir la página con HTTPS o desde localhost.';
    }
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return 'Este navegador no permite usar el micrófono.';
    }
    if (typeof MediaRecorder === 'undefined') {
      return 'Este navegador no puede grabar audio.';
    }
    return '';
  }

  function pickMimeType() {
    if (typeof MediaRecorder.isTypeSupported !== 'function') return '';
    return MIME_CANDIDATES.find((type) => MediaRecorder.isTypeSupported(type)) || '';
  }

  function micError(err) {
    const name = err && err.name;
    if (name === 'NotAllowedError' || name === 'SecurityError') {
      fail('Permiso de micrófono denegado. Habilitarlo en la configuración del sitio del navegador y recargar la página.',
        'Micrófono bloqueado');
    } else if (name === 'NotFoundError' || name === 'OverconstrainedError') {
      fail('No se encontró ningún micrófono.');
    } else if (name === 'NotReadableError') {
      fail('El micrófono está en uso por otra aplicación o no responde.');
    } else {
      fail('No se pudo abrir el micrófono.');
    }
  }

  function streamIsLive() {
    return stream && stream.getAudioTracks().some((track) => track.readyState === 'live');
  }

  function releaseMic() {
    clearTimeout(releaseTimer);
    if (stream) stream.getTracks().forEach((track) => track.stop());
    stream = null;
  }

  function scheduleMicRelease() {
    clearTimeout(releaseTimer);
    releaseTimer = setTimeout(releaseMic, RELEASE_MIC_AFTER_MS);
  }

  // iOS only lets an audio element play later, outside a user gesture, if it
  // already played something during one. A 50 ms silent WAV does that.
  function silentWAV() {
    const samples = 400; // 50 ms at 8 kHz, 16-bit mono
    const view = new DataView(new ArrayBuffer(44 + samples * 2));
    const text = (offset, value) => {
      for (let i = 0; i < value.length; i++) view.setUint8(offset + i, value.charCodeAt(i));
    };
    text(0, 'RIFF');
    view.setUint32(4, 36 + samples * 2, true);
    text(8, 'WAVEfmt ');
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, 8000, true);
    view.setUint32(28, 16000, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    text(36, 'data');
    view.setUint32(40, samples * 2, true);
    return new Blob([view], { type: 'audio/wav' });
  }

  function unlockAudio() {
    if (silentURL || replyURL) return;
    silentURL = URL.createObjectURL(silentWAV());
    player.src = silentURL;
    player.play().catch(() => {});
  }

  function isSilent() {
    return Boolean(silentURL) && player.currentSrc === silentURL;
  }

  async function press() {
    if (pressed || talk.disabled) return;
    pressed = true;
    showNotice('');
    if (!player.paused && !isSilent()) player.pause();
    unlockAudio();

    if (!streamIsLive()) {
      setState('idle', 'Pidiendo acceso al micrófono…');
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        });
      } catch (err) {
        pressed = false;
        micError(err);
        return;
      }
      if (!pressed) {
        // Released while the permission prompt was open.
        setState('idle', 'Micrófono listo. Mantener presionado para hablar.');
        scheduleMicRelease();
        return;
      }
    }
    startRecording();
  }

  function startRecording() {
    clearTimeout(releaseTimer);
    const mimeType = pickMimeType();
    let rec;
    try {
      rec = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
    } catch (err) {
      pressed = false;
      fail('No se pudo iniciar la grabación en este navegador.');
      return;
    }
    const parts = [];
    rec.addEventListener('dataavailable', (event) => {
      if (event.data && event.data.size > 0) parts.push(event.data);
    });
    rec.parts = parts;
    recorder = rec;
    rec.start();
    recordStart = performance.now();
    setState('recording', 'Escuchando… soltar para enviar');
    maxTimer = setTimeout(release, MAX_RECORD_MS);
  }

  function release() {
    pressed = false;
    if (!recorder || recorder.state !== 'recording') return;
    clearTimeout(maxTimer);
    const rec = recorder;
    const duration = performance.now() - recordStart;
    recorder = null;
    rec.addEventListener('stop', () => finish(rec, duration), { once: true });
    rec.stop();
  }

  // cancel drops a recording in progress without sending it.
  function cancel() {
    pressed = false;
    clearTimeout(maxTimer);
    if (recorder && recorder.state === 'recording') recorder.stop();
    recorder = null;
    if (state === 'recording') setState('idle', READY);
  }

  function finish(rec, duration) {
    scheduleMicRelease();
    if (duration < MIN_PRESS_MS) {
      setState('idle', 'Pulsación muy corta: mantener presionado mientras se habla.');
      return;
    }
    const type = rec.mimeType || (rec.parts[0] && rec.parts[0].type) || '';
    const blob = new Blob(rec.parts, type ? { type } : {});
    if (blob.size === 0) {
      fail('No se grabó audio. Volver a intentar.');
      return;
    }
    sendTurn(blob);
  }

  function errorMessage(status, body) {
    const stage = STAGE_NAMES[body && body.stage] || 'servidor';
    switch (status) {
      case 400:
        return 'El servidor no pudo leer el audio grabado.';
      case 413:
        return 'La grabación es demasiado larga. Probar con una pregunta más corta.';
      case 422:
        return 'No se detectó voz. Hablar más cerca del micrófono y volver a intentar.';
      case 502:
        return `Falló un servicio interno (${stage}). Volver a intentar en un momento.`;
      case 504:
        return 'La respuesta tardó demasiado. Volver a intentar.';
      default:
        return `Error inesperado del servidor (${status}).`;
    }
  }

  function decodedHeader(response, name) {
    const raw = response.headers.get(name) || '';
    try {
      return decodeURIComponent(raw);
    } catch (err) {
      return raw;
    }
  }

  async function sendTurn(blob) {
    setState('processing', 'Procesando…');
    let response;
    let audio;
    try {
      response = await fetch('/v1/turn', {
        method: 'POST',
        headers: { 'Content-Type': blob.type || 'application/octet-stream' },
        body: blob,
      });
      if (!response.ok) {
        let body = null;
        try {
          body = await response.json();
        } catch (err) {
          // Not a JSON error body; the status is enough.
        }
        fail(errorMessage(response.status, body));
        return;
      }
      audio = await response.blob();
    } catch (err) {
      fail('No se pudo contactar al servidor. Revisar la conexión.');
      return;
    }
    const timings = {};
    for (const [key] of TIMINGS) timings[key] = Number(response.headers.get(`X-Timing-${key}`));
    addTurn(decodedHeader(response, 'X-Transcript'), decodedHeader(response, 'X-Reply'), timings);
    playReply(audio);
  }

  function formatMs(ms) {
    if (!Number.isFinite(ms)) return '–';
    if (ms < 1000) return `${Math.round(ms)} ms`;
    return `${(ms / 1000).toFixed(1).replace('.', ',')} s`;
  }

  function bubble(kind, who, text) {
    const p = document.createElement('p');
    p.className = `bubble ${kind}`;
    const label = document.createElement('span');
    label.className = 'who';
    label.textContent = who;
    p.append(label, document.createTextNode(text));
    return p;
  }

  function addTurn(transcript, reply, timings) {
    empty.hidden = true;
    const turn = document.createElement('article');
    turn.className = 'turn';
    const times = document.createElement('p');
    times.className = 'timings';
    times.textContent = TIMINGS.map(([key, label]) => `${label} ${formatMs(timings[key])}`).join(' · ');
    turn.append(bubble('user', 'Pregunta', transcript), bubble('assistant', 'Respuesta', reply), times);
    log.append(turn);
    while (log.querySelectorAll('.turn').length > MAX_TURNS) log.querySelector('.turn').remove();
    turn.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }

  function playReply(blob) {
    if (replyURL) URL.revokeObjectURL(replyURL);
    if (silentURL) {
      URL.revokeObjectURL(silentURL);
      silentURL = '';
    }
    replyURL = URL.createObjectURL(blob);
    player.src = replyURL;
    player.hidden = false;
    setState('idle', 'Respuesta lista');
    player.play().catch(() => {
      setState('idle', 'Respuesta lista: tocar ▶ para escucharla.');
    });
  }

  player.addEventListener('playing', () => {
    if (!isSilent()) setState('playing', 'Respondiendo…');
  });
  player.addEventListener('pause', () => {
    if (state === 'playing') setState('idle', READY);
  });
  player.addEventListener('ended', () => {
    if (state === 'playing') setState('idle', READY);
  });

  talk.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    event.preventDefault();
    press();
  });
  for (const type of ['pointerup', 'pointercancel', 'pointerleave']) {
    talk.addEventListener(type, release);
  }
  talk.addEventListener('contextmenu', (event) => event.preventDefault());

  // The space bar works as the button while focus is not on another control.
  function spaceTarget(event) {
    if (event.code !== 'Space' && event.key !== ' ') return false;
    const target = event.target;
    return target === talk || !(target instanceof Element) ||
      !target.closest('button, audio, a, input, textarea, select, [contenteditable]');
  }
  document.addEventListener('keydown', (event) => {
    if (!spaceTarget(event)) return;
    event.preventDefault();
    if (!event.repeat) press();
  });
  document.addEventListener('keyup', (event) => {
    if (!spaceTarget(event)) return;
    event.preventDefault();
    release();
  });

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      cancel();
      releaseMic();
    }
  });

  const reason = unavailableReason();
  if (reason) {
    showNotice(reason);
    setState('unavailable', 'Micrófono no disponible');
  } else {
    setState('idle', READY);
  }
})();
