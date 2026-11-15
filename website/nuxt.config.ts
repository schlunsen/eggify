// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },

  // Static site generation for GitHub Pages
  ssr: false,

  app: {
    baseURL: '/eggify/',
    head: {
      title: 'Eggify',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'description', content: 'The coolest cats code in sandboxes.' },

        // OpenGraph
        { property: 'og:site_name', content: 'Eggify' },
        { property: 'og:title', content: 'Eggify: the coolest cats code in sandboxes' },
        { property: 'og:description', content: 'The coolest cats code in sandboxes.' },
        { property: 'og:type', content: 'website' },

        // Twitter Card
        { name: 'twitter:card', content: 'summary_large_image' },
        { name: 'twitter:title', content: 'Eggify: the coolest cats code in sandboxes' },
        { name: 'twitter:description', content: 'The coolest cats code in sandboxes.' }
      ],
      link: [
        { rel: 'icon', type: 'image/x-icon', href: '/eggify/favicon.ico' },
        { rel: 'preconnect', href: 'https://fonts.googleapis.com' },
        { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' },
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&display=swap' }
      ]
    }
  },
})
