'use strict';
window.addEventListener('DOMContentLoaded', function () {
  window.ui = SwaggerUIBundle({
    url: '/api/openapi.json',
    dom_id: '#swagger-ui',
    validatorUrl: null,
    queryConfigEnabled: false,
    persistAuthorization: false,
    deepLinking: true,
    supportedSubmitMethods: ['get', 'post', 'put', 'patch', 'delete'],
    requestInterceptor: async function (request) {
      if (new URL(request.url, window.location.href).origin !== window.location.origin) throw new Error('Only same-origin API requests are allowed.');
      const bearer = request.headers && (request.headers.Authorization || request.headers.authorization);
      request.credentials = bearer ? 'omit' : 'same-origin';
      if (!bearer && !['GET', 'HEAD'].includes((request.method || 'GET').toUpperCase())) {
        const session = await fetch('/api/v1/session', { credentials: 'same-origin', cache: 'no-store' });
        if (session.ok) request.headers['X-CSRF-Token'] = (await session.json()).csrf_token;
      }
      return request;
    },
    displayRequestDuration: true,
    presets: [SwaggerUIBundle.presets.apis],
    layout: 'BaseLayout'
  });
});
