export interface ProjectPreview {
  tasks: [string[], string[]];
  details: [string[], string[]];
  progress: [number, number];
}

export const projects: Record<string, ProjectPreview> = {
  fieldnotes: {
    tasks: [
      ['Give the homepage', 'a little personality.'],
      ['Make search feel', 'instant.'],
    ],
    details: [
      ['↳ src/pages/index.vue', 'Building the first impression'],
      ['↳ api/search.ts', 'Connecting the moving parts'],
    ],
    progress: [65, 43],
  },
  shop: {
    tasks: [
      ['Build a storefront', 'worth wandering into.'],
      ['Give every product', 'a place on the shelf.'],
    ],
    details: [
      ['↳ src/pages/store.vue', 'Shaping the shopfront'],
      ['↳ api/products.ts', 'Organizing the catalog'],
    ],
    progress: [38, 81],
  },
};
