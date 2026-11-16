import { Fragment, type CSSProperties, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { locale, t } from '../i18n';
import './admin.css';

/** Pieces the administration screens share. */

export const number = (n: number) => n.toLocaleString(locale);

/** A date with its time: "Nov 4, 9:11 PM", with the year when it is not this year. */
export function dateTime(iso: string): string {
  const date = new Date(iso);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleString(locale, sameYear
    ? { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }
    : { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

export function When({ value, empty }: { value?: string | null; empty?: string }) {
  if (!value) return empty ? <span className="subtle">{empty}</span> : <Dash />;
  return <time dateTime={value} title={new Date(value).toLocaleString(locale, { dateStyle: 'full', timeStyle: 'long' })}>{dateTime(value)}</time>;
}

export { relative } from '../format';

/** Parts of a phone row's second line, " · " between those present. */
export function Joined({ parts }: { parts: ReactNode[] }) {
  const present = parts.filter(part => part !== null && part !== undefined && part !== false && part !== '');
  return <>{present.map((part, i) => <Fragment key={i}>{i > 0 && ' · '}<span className="adm-part">{part}</span></Fragment>)}</>;
}

/** A missing value. */
export const Dash = () => <span className="subtle">–</span>;

/** Words from an identifier, in sentence case: "catalog_refresh" → "Catalog refresh". */
export function sentence(value: string): string {
  const plain = value.replaceAll(/[._]/g, ' ');
  return plain.charAt(0).toUpperCase() + plain.slice(1);
}

export type StripItem = { label: string; value: string; sub?: ReactNode; bad?: boolean; to?: string };

/** Headline figures in one container split by hairlines: label over value, no icons. */
export function Strip({ label, items }: { label: string; items: StripItem[] }) {
  return <dl className={`adm-strip cells-${items.length}`} aria-label={label} style={{ '--cells': items.length } as CSSProperties}>
    {items.map(item => <div key={item.label}>
      <dt>{item.label}</dt>
      <dd className="adm-strip-value">{item.to ? <Link to={item.to}>{item.value}</Link> : item.value}</dd>
      {item.sub && <dd className={`adm-strip-sub ${item.bad ? 'bad' : ''}`}>{item.sub}</dd>}
    </div>)}
  </dl>;
}

export const roleName = (role: string) => ({ member: t('Member'), admin: t('Administrator'), owner: t('Owner') } as Record<string, string>)[role] ?? sentence(role);
