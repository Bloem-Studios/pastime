import { Link } from 'react-router-dom';
import { CalendarRange, ChevronRight } from 'lucide-react';
import { t } from '../i18n';
import { reviewYear } from '../statsPolicy';
export type { CardField } from '../media';
export { reviewYear };

/** The Year in Review banner, shown in December and January. */
export function ReviewBanner() {
  const month = new Date().getUTCMonth(); const year = reviewYear();
  if (month !== 11 && month !== 0) return null;
  return <Link className="review-banner" to={`/review/${year}`}><CalendarRange aria-hidden /><span><strong>{t('Your {year} in review', { year })}</strong>
    <span className="subtle">{month === 11 ? t('Your year so far: time spent, streaks and the titles you spent it on.') : t('Your year in stories is ready.')}</span></span><ChevronRight aria-hidden /></Link>;
}
