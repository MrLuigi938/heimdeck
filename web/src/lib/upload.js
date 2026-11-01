import { post } from './api.js';
import { t } from './i18n.svelte.js';

// Uploads a picture as a tile icon or background. It is scaled down in the
// browser first – a 20 MB photo from the phone becomes a few hundred KB – so
// the dashboard stays fast. SVG icons are sent as they are.
const MAX = { icon: 256, background: 2560 };

function toBase64(blob) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).split(',')[1]);
    r.onerror = () => reject(r.error);
    r.readAsDataURL(blob);
  });
}

const encode = (canvas, type, quality) => new Promise((resolve) => canvas.toBlob(resolve, type, quality));

async function shrink(file, kind) {
  let bitmap;
  try {
    bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
  } catch {
    throw new Error(t('Dieses Bild kann der Browser nicht lesen – bitte als JPEG, PNG oder WebP speichern.'));
  }
  const scale = Math.min(1, MAX[kind] / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  const ctx = canvas.getContext('2d');
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close?.();
  // WebP keeps transparency for icons and is small; older Safari falls back.
  let blob = await encode(canvas, 'image/webp', kind === 'icon' ? 0.92 : 0.85);
  if (!blob || blob.type !== 'image/webp') blob = await encode(canvas, kind === 'icon' ? 'image/png' : 'image/jpeg', 0.85);
  return blob;
}

/** @returns {Promise<{id: string, url: string}>} */
export async function uploadImage(file, kind) {
  if (!file?.type?.startsWith('image/')) throw new Error(t('Bitte eine Bilddatei wählen'));
  const svg = file.type === 'image/svg+xml';
  if (svg && kind !== 'icon') throw new Error(t('SVG geht nur als Icon'));
  const blob = svg ? file : await shrink(file, kind);
  return post('/uploads', { kind, name: file.name, data: await toBase64(blob) });
}
