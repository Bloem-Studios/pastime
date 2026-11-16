import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { BookCopy, Check, ScanBarcode } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { useRefresh } from '../state';
import { ErrorNote, Failed, Loading, Modal, useAction } from '../ui';
import { t } from '../i18n';

type Edition = Schema['Edition'];

function editionFacts(e: Edition) {
  return [e.format, e.publisher, e.year, e.pages ? t('{pages} pages', { pages: e.pages }) : null, e.language ? e.language.toUpperCase() : null].filter(Boolean).join(' · ');
}

/** The edition a person reads: its page count and cover apply to them only. */
export function EditionSection({ item, entry }: { item: Schema['CatalogItem']; entry: Schema['Entry'] }) {
  const [choosing, setChoosing] = useState(false); const refresh = useRefresh();
  const clear = useAction(async (_: void, key) => { unwrap(await api.PUT('/api/v1/library/{entryID}/edition', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: entry.version, edition_id: null } })); await refresh(); });
  const ed = entry.edition;
  return <section className="detail-section"><h2>{t('Edition')}</h2>
    {ed ? <div className="edition-current">
      {ed.image_url ? <img src={ed.image_url} alt="" loading="lazy" referrerPolicy="no-referrer" /> : <span className="edition-thumb"><BookCopy aria-hidden /></span>}
      <div><strong>{ed.title || item.title}</strong><span className="subtle">{editionFacts(ed) || 'Edition details unknown'}</span>{ed.isbn && <span className="subtle">{t('ISBN')} {ed.isbn}</span>}</div>
      <div className="actions"><button className="button" onClick={() => setChoosing(true)}>{t('Change')}</button><button className="button ghost" disabled={clear.pending} onClick={() => void clear.run()}>{t('Use the work’s details')}</button></div>
    </div> : <><p className="subtle">{t('Pick the edition you read to use its page count and cover.')}</p><button className="button" onClick={() => setChoosing(true)}><BookCopy aria-hidden />{t('Choose edition')}</button></>}
    <ErrorNote error={clear.error} />
    {choosing && <EditionDialog item={item} entry={entry} onClose={() => setChoosing(false)} />}
  </section>;
}

function EditionDialog({ item, entry, onClose }: { item: Schema['CatalogItem']; entry: Schema['Entry']; onClose: () => void }) {
  const refresh = useRefresh(); const [filter, setFilter] = useState('');
  const editions = useQuery({ queryKey: ['editions', item.id], staleTime: 3_600_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/editions', { signal, params: { path: { catalogID: item.id } } })).items });
  const choose = useAction(async (id: string, key) => { unwrap(await api.PUT('/api/v1/library/{entryID}/edition', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: entry.version, edition_id: id } })); await refresh(); onClose(); });
  const needle = filter.trim().toLowerCase();
  const shown = (editions.data ?? []).filter(e => !needle || [e.title, e.publisher, e.isbn, e.format, e.year].some(v => v.toLowerCase().includes(needle)));
  return <Modal title={t('Choose edition')} description={t('Editions of {title}. Its pages become your total and its cover your artwork.', { title: item.title })} onClose={onClose} busy={choose.pending} wide>
    {editions.isPending ? <Loading label={t('Loading editions')} /> : editions.error ? <Failed error={editions.error} retry={() => void editions.refetch()} />
      : !editions.data.length ? <p className="subtle">{t('The provider lists no editions for this title.')}</p>
      : <>
        <div className="field"><label htmlFor="edition-filter">{t('Filter')}</label><input id="edition-filter" type="search" placeholder={t('Publisher, year, ISBN or format')} value={filter} onChange={e => setFilter(e.target.value)} /></div>
        <ul className="edition-list">{shown.slice(0, 60).map(e => <li key={e.id}><button className="edition-row" disabled={choose.pending} aria-pressed={entry.edition?.id === e.id} onClick={() => void choose.run(e.id)}>
          {e.image_url ? <img src={e.image_url} alt="" loading="lazy" referrerPolicy="no-referrer" /> : <span className="edition-thumb"><BookCopy aria-hidden /></span>}
          <span className="edition-text"><strong>{e.title || item.title}</strong><span className="subtle">{editionFacts(e) || 'No details'}</span>{e.isbn && <span className="subtle">{t('ISBN')} {e.isbn}</span>}</span>
          {entry.edition?.id === e.id && <Check aria-hidden className="edition-check" />}
        </button></li>)}</ul>
        {shown.length === 0 && <p className="subtle">{t('No edition matches.')}</p>}
      </>}
    <ErrorNote error={choose.error} />
  </Modal>;
}

type Detector = { detect: (source: HTMLVideoElement) => Promise<{ rawValue: string }[]> };
type DetectorClass = { new(options: { formats: string[] }): Detector; getSupportedFormats?: () => Promise<string[]> };
const BarcodeDetectorClass = (globalThis as unknown as { BarcodeDetector?: DetectorClass }).BarcodeDetector;

/** Scans a book's ISBN barcode with the camera where the browser can read barcodes. */
export function BarcodeButton({ onCode }: { onCode: (isbn: string) => void }) {
  const [open, setOpen] = useState(false);
  if (!BarcodeDetectorClass || !navigator.mediaDevices?.getUserMedia) return null;
  return <>
    <button type="button" className="icon-button search-scan" aria-label={t('Scan an ISBN barcode')} title={t('Scan barcode')} onClick={() => setOpen(true)}><ScanBarcode aria-hidden /></button>
    {open && <Scanner onClose={() => setOpen(false)} onCode={code => { setOpen(false); onCode(code); }} />}
  </>;
}

function Scanner({ onClose, onCode }: { onClose: () => void; onCode: (isbn: string) => void }) {
  const video = useRef<HTMLVideoElement>(null); const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let stream: MediaStream | null = null; let stopped = false; let timer = 0;
    const detector = new BarcodeDetectorClass!({ formats: ['ean_13', 'ean_8', 'upc_a'] });
    (async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' }, audio: false });
        if (stopped || !video.current) { stream.getTracks().forEach(track => track.stop()); return; }
        video.current.srcObject = stream; await video.current.play();
        const scan = async () => {
          if (stopped || !video.current) return;
          try {
            const detected = await detector.detect(video.current);
            if (stopped) return;
            for (const code of detected) {
              const digits = code.rawValue.replace(/\D/g, '');
              if (digits.length === 13 && (digits.startsWith('978') || digits.startsWith('979'))) { onCode(digits); return; }
            }
          } catch { /* A frame that cannot be read is skipped. */ }
          timer = window.setTimeout(scan, 250);
        };
        void scan();
      } catch { stream?.getTracks().forEach(track => track.stop()); if (!stopped) setError(t('The camera could not be opened. Allow camera access, or type the ISBN instead.')); }
    })();
    return () => { stopped = true; window.clearTimeout(timer); stream?.getTracks().forEach(item => item.stop()); };
  }, [onCode]);
  return <Modal title={t('Scan ISBN')} description={t('Point the camera at the barcode on the back of the book.')} onClose={onClose}>
    {error ? <p className="error-text" role="alert">{error}</p> : <div className="scanner"><video ref={video} muted playsInline aria-label={t('Camera preview')} /><span className="scanner-guide" aria-hidden /></div>}
  </Modal>;
}
