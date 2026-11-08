(() => {
  if (!window.Chart) return;

  Chart.defaults.font.family = getComputedStyle(document.body).fontFamily;
  Chart.register({
    id: 'weirdstatsTheme',
    beforeUpdate(chart) {
      const style = getComputedStyle(document.documentElement);
      const text = style.getPropertyValue('--ink-muted').trim();
      const border = style.getPropertyValue('--border').trim();
      chart.options.color = text;
      Object.values(chart.config.options.scales || {}).forEach(scale => {
        scale.ticks = { ...scale.ticks, color: text };
        scale.title = { ...scale.title, color: text };
        scale.grid = { ...scale.grid, color: border };
        scale.border = { ...scale.border, color: border };
      });
    }
  });

  new MutationObserver(() => {
    Object.values(Chart.instances).forEach(chart => chart.update('none'));
  }).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme']
  });
})();
