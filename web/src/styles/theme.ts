import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * Visual language: Apple / Ubiquiti (ui.com) inspired minimalism.
 * - near-white surfaces, 1px hairline borders, generous whitespace
 * - near-black text with a strict grey scale, ONE restrained blue accent
 * - system font stack, small radii, almost no shadows or gradients
 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#0066cc',
    primaryColorHover: '#0a76d8',
    primaryColorPressed: '#0055ab',
    primaryColorSuppl: '#0066cc',
    infoColor: '#0066cc',
    infoColorHover: '#0a76d8',
    infoColorPressed: '#0055ab',
    successColor: '#1d9a68',
    warningColor: '#c7871a',
    errorColor: '#d64545',
    bodyColor: '#ffffff',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    tableColor: '#ffffff',
    inputColor: '#ffffff',
    actionColor: '#f7f7f9',
    borderColor: '#e6e6eb',
    dividerColor: '#efeff2',
    hoverColor: 'rgba(0, 102, 204, 0.055)',
    textColorBase: '#1d1d1f',
    textColor1: '#1d1d1f',
    textColor2: '#6e6e73',
    textColor3: '#8e8e93',
    placeholderColor: '#aeaeb2',
    borderRadius: '10px',
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'SF Pro Text', 'SF Pro Display', 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif",
    fontFamilyMono:
      "'SF Mono', 'Cascadia Code', 'JetBrains Mono', Consolas, 'Courier New', monospace",
  },
  Menu: {
    itemColorActive: 'rgba(0, 102, 204, 0.075)',
    itemColorActiveHover: 'rgba(0, 102, 204, 0.10)',
    itemTextColorActive: '#0066cc',
    itemTextColorActiveHover: '#0066cc',
    itemTextColorActiveChild: '#0066cc',
    itemIconColorActive: '#0066cc',
    itemIconColorActiveHover: '#0066cc',
    itemIconColorActiveChild: '#0066cc',
    itemColorActiveCollapsed: 'rgba(0, 102, 204, 0.10)',
  },
  DataTable: {
    thColor: '#fafafb',
    tdColor: '#ffffff',
    tdColorHover: 'rgba(0, 102, 204, 0.03)',
    borderColor: '#eeeef2',
    thTextColor: '#6e6e73',
  },
  Layout: {
    siderColor: '#fbfbfd',
    headerColor: '#ffffff',
    color: '#ffffff',
  },
  Card: {
    borderColor: '#e6e6eb',
    color: '#ffffff',
  },
  Input: {
    color: '#ffffff',
    border: '1px solid #dcdce1',
  },
}

/** Single source of truth for inline styles / charts. */
export const PRIMARY_COLOR = '#0066cc'
