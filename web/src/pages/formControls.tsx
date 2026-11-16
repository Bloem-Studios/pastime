import { useState, type InputHTMLAttributes } from 'react';
import { Eye, EyeOff } from 'lucide-react';
import { t } from '../i18n';
import './form-controls.css';

/** Reveal only the value being entered; existing stored secrets are never fetched. */
export function PasswordInput(props: InputHTMLAttributes<HTMLInputElement>) {
  const [revealed, setRevealed] = useState(false);
  return <div className="password-control"><input {...props} type={revealed ? 'text' : 'password'} /><button type="button" className="icon-button" aria-label={revealed ? t('Hide password') : t('Show password')} aria-pressed={revealed} aria-controls={props.id} onClick={() => setRevealed(v => !v)}>{revealed ? <EyeOff aria-hidden /> : <Eye aria-hidden />}</button></div>;
}
