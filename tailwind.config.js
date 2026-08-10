module.exports = {
  darkMode: 'class',
  content: ['./internal/web/*.html', './internal/web/*.js'],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          '-apple-system', 'BlinkMacSystemFont', '"PingFang SC"', '"Helvetica Neue"',
          '"Segoe UI"', '"Microsoft YaHei"', 'system-ui', 'sans-serif',
        ],
      },
    },
  },
  plugins: [],
};
