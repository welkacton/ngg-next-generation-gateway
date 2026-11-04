import type { GlobalThemeOverrides } from 'naive-ui'

/** 主色：青蓝 #36adcb，暗色主题下的细节微调。 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#36adcb',
    primaryColorHover: '#5cc1da',
    primaryColorPressed: '#2a93ad',
    primaryColorSuppl: '#36adcb',
    infoColor: '#36adcb',
    infoColorHover: '#5cc1da',
    infoColorPressed: '#2a93ad',
    successColor: '#3fb984',
    warningColor: '#e6a649',
    errorColor: '#e88080',
    bodyColor: '#0f1419',
    cardColor: '#171d24',
    modalColor: '#1b222a',
    popoverColor: '#1b222a',
    tableColor: '#171d24',
    inputColor: '#11171d',
    actionColor: '#11171d',
    borderColor: '#2b343e',
    dividerColor: '#2b343e',
    hoverColor: 'rgba(54, 173, 203, 0.10)',
    textColorBase: '#e6edf3',
    textColor1: '#e9f0f5',
    textColor2: '#b9c5cf',
    textColor3: '#8795a1',
    placeholderColor: '#6d7b88',
    borderRadius: '6px',
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'Microsoft YaHei', 'PingFang SC', sans-serif",
    fontFamilyMono:
      "'Cascadia Code', 'JetBrains Mono', Consolas, 'Courier New', monospace",
  },
  Menu: {
    itemColorActive: 'rgba(54, 173, 203, 0.14)',
    itemColorActiveHover: 'rgba(54, 173, 203, 0.18)',
    itemTextColorActive: '#5cc1da',
    itemTextColorActiveHover: '#5cc1da',
    itemTextColorActiveChild: '#5cc1da',
    itemIconColorActive: '#36adcb',
    itemIconColorActiveHover: '#36adcb',
    itemIconColorActiveChild: '#36adcb',
    itemColorActiveCollapsed: 'rgba(54, 173, 203, 0.18)',
  },
  DataTable: {
    thColor: '#1d252e',
    tdColor: '#171d24',
    tdColorHover: 'rgba(54, 173, 203, 0.06)',
    borderColor: '#2b343e',
    thTextColor: '#b9c5cf',
  },
  Layout: {
    siderColor: '#121820',
    headerColor: '#121820',
    color: '#0f1419',
  },
  Card: {
    borderColor: '#2b343e',
    color: '#171d24',
  },
  Input: {
    color: '#11171d',
    border: '1px solid #2b343e',
  },
}

/** 供 TS 复用的主色常量（如图表、内联样式）。 */
export const PRIMARY_COLOR = '#36adcb'
