import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

const config: Config = {
  title: 'Shoulders',
  tagline: 'Internal Developer Platform on Kubernetes — stand on the shoulders of giants',
  favicon: 'img/favicon.svg',

  future: {
    v4: true,
  },

  // Production URL for shoulders.juanherreros.com (custom domain on this repo).
  url: 'https://shoulders.juanherreros.com',
  baseUrl: '/',

  organizationName: 'jherreros',
  projectName: 'shoulders',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/jherreros/shoulders/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Shoulders',
      logo: {
        alt: 'Shoulders Logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          to: '/#quickstart',
          label: 'Quickstart',
          position: 'left',
        },
        {
          href: 'https://github.com/jherreros/shoulders',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Getting Started', to: '/docs/getting-started/quickstart'},
            {label: 'Concepts', to: '/docs/concepts/workspaces'},
            {label: 'CLI Reference', to: '/docs/cli/overview'},
          ],
        },
        {
          title: 'Platform',
          items: [
            {label: 'API Reference', to: '/docs/api/overview'},
            {label: 'MCP Server', to: '/docs/integrations/mcp-server'},
            {label: 'Portal Plugin', to: '/docs/integrations/portal'},
          ],
        },
        {
          title: 'More',
          items: [
            {label: 'Main site', href: 'https://juanherreros.com'},
            {label: 'GitHub', href: 'https://github.com/jherreros/shoulders'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Juan Herreros. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'yaml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
