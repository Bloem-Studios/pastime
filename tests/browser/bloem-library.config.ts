import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'.',testMatch:'bloem-library.spec.ts',workers:1,retries:0,timeout:30000,reporter:'list',use:{baseURL:'http://127.0.0.1:18169',browserName:'chromium',trace:'retain-on-failure'}});
