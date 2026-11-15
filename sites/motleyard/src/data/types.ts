export interface Feature {
  title: string;
  description: string;
}
export interface Step {
  title: string;
  description: string;
}
export interface FAQ {
  question: string;
  answer: string;
}
export interface PageContent {
  features: Feature[];
  steps: Step[];
  faqs: FAQ[];
}
