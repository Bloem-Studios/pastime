import {defineConfig} from '@playwright/test';
const baseURL='http://localhost:18164';
export default defineConfig({testDir:'.',testMatch:'managed-profiles.spec.ts',workers:1,fullyParallel:false,retries:0,maxFailures:1,timeout:30000,reporter:'list',use:{baseURL,browserName:'chromium',storageState:{cookies:[],origins:[]},trace:'retain-on-failure'}});
