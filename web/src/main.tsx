import { loadLocale } from './i18n';

// Translations load first, so text defined when modules load is already translated.
void loadLocale().finally(() => import('./app'));
