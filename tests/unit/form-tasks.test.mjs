import { test } from 'node:test';
import assert from 'node:assert/strict';
import { integrationTask, countryOptions } from '../../web/src/pages/formTasks.ts';

test('existing source deep links open import and export without overriding explicit tasks', () => {
  assert.equal(integrationTask(new URLSearchParams('source=letterboxd')), 'transfer');
  assert.equal(integrationTask(new URLSearchParams('section=snapshots&source=native')), 'snapshots');
  assert.equal(integrationTask(new URLSearchParams('service=komga')), 'services');
  assert.equal(integrationTask(new URLSearchParams('section=unknown')), 'services');
});
test('country chooser supplies ISO codes and named, sorted searchable options', () => {
  const countries = countryOptions('en');
  assert.equal(countries.find(c => c[0] === 'NL')?.[1], 'Netherlands');
  assert.equal(countries.find(c => c[0] === 'US')?.[1], 'United States');
  assert.ok(countries.length > 240);
  assert.equal(new Set(countries.map(c => c[0])).size, countries.length);
  assert.deepEqual(countries, [...countries].sort((a,b) => a[1].localeCompare(b[1], 'en')));
});
