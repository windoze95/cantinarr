import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://docs.cantinarr.com',
  trailingSlash: 'always',
  integrations: [starlight({
    title: 'Cantinarr Docs',
    description: 'Clear, practical help for setting up Cantinarr, managing your libraries, and getting everyone connected.',
    favicon: '/favicon.png',
    logo: { src: './public/icon.png', replacesTitle: false },
    customCss: ['./src/styles/docs.css'],
    social: [
      { icon: 'github', label: 'Source on GitHub', href: 'https://github.com/windoze95/cantinarr' },
      { icon: 'discord', label: 'Cantinarr community', href: 'https://discord.gg/zAgRwGwmVB' },
    ],
    editLink: { baseUrl: 'https://github.com/windoze95/cantinarr/edit/main/docs-site/' },
    components: {
      PageTitle: './src/components/PageTitle.astro',
      Footer: './src/components/Footer.astro',
    },
    expressiveCode: { themes: ['github-dark', 'github-light'] },
    tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
    credits: false,
    sidebar: [
      { label: 'Documentation home', link: '/' },
      { label: 'Start here', items: [{ autogenerate: { directory: 'start' } }] },
      { label: 'Install & maintain', items: [{ autogenerate: { directory: 'install' } }], collapsed: true },
      { label: 'Use Cantinarr', items: [{ autogenerate: { directory: 'use' } }], collapsed: true },
      { label: 'Manage your server', items: [{ autogenerate: { directory: 'admin' } }], collapsed: true },
      { label: 'Connect your services', items: [
        'integrations', 'integrations/radarr-sonarr',
        'integrations/guides/books', 'integrations/guides/music',
        'integrations/download-clients', 'integrations/media-servers',
        'integrations/audiobookshelf', 'integrations/guides/apple-tv',
        'integrations/instant-updates', 'integrations/push', 'integrations/discord',
        'integrations/monitoring', 'integrations/tdarr', 'integrations/guides/oidc',
        'integrations/guides/plex-sign-in', 'integrations/discovery-providers',
        'integrations/outbound-proxy', 'integrations/mcp', 'integrations/seerr-api',
      ], collapsed: true },
      { label: 'Fix a problem', items: [{ autogenerate: { directory: 'troubleshooting' } }], collapsed: true },
      { label: 'Reference', items: [
        'reference/settings', 'reference/generated/settings', 'reference/glossary',
        'reference/permissions', 'reference/generated/environment', 'reference/coverage',
        { label: 'API routes', items: [{ autogenerate: { directory: 'reference/generated/api' } }], collapsed: true },
        { label: 'App behavior', items: [{ autogenerate: { directory: 'reference/generated/app' } }], collapsed: true },
        { label: 'Server behavior', items: [{ autogenerate: { directory: 'reference/generated/architecture' } }], collapsed: true },
        'reference/generated/privacy',
      ], collapsed: true },
      { label: 'Contribute', items: [
        'contributing/development', 'contributing/testing', 'contributing/documentation',
        'contributing/generated/releases',
      ], collapsed: true },
    ],
    head: [{ tag: 'meta', attrs: { name: 'theme-color', content: '#17110d' } }],
  })],
});
