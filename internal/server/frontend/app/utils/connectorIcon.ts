/** Maps a connector's icon name (from the catalog) to an Icon component name. */
export function connectorIconName(icon?: string): string {
  const iconMap: Record<string, string> = {
    'brain': 'lucide:brain',
    'sparkles': 'lucide:sparkles',
    'sparkle': 'lucide:sparkle',
    'crystal-ball': 'lucide:gem',
    'wind': 'lucide:wind',
    'route': 'lucide:route',
    'github': 'lucide:github',
    'book-open': 'lucide:book-open',
    'layers': 'lucide:layers',
    'hash': 'lucide:hash',
    'message-circle': 'lucide:message-circle',
    'mail': 'lucide:mail',
    'webhook': 'lucide:webhook',
    'triangle': 'lucide:triangle',
    'database': 'lucide:database',
    'credit-card': 'lucide:credit-card',
    'git-branch': 'lucide:git-branch',
    'shield': 'lucide:shield',
    'alert-triangle': 'lucide:alert-triangle',
    'smile': 'lucide:smile',
  }
  return iconMap[icon || ''] || 'lucide:plug'
}
