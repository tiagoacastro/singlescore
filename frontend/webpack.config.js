/* eslint-env node */
// SingleScore frontend bundler. Explicitly uses Webpack 5 + SWC
// (not Vite) per the project's technology requirements.

const path = require('path');
const HtmlWebpackPlugin = require('html-webpack-plugin');
const webpack = require('webpack');

module.exports = (_env, argv) => {
  const isProd = argv.mode === 'production';
  // The runtime GraphQL endpoint. Default to a *relative* path so the
  // webpack-dev-server proxy (or any reverse proxy in prod) handles the
  // hop to the backend and we avoid CORS entirely.
  const graphqlEndpoint = process.env.FRONTEND_GRAPHQL_ENDPOINT || '/graphql';
  // Where the dev-server proxy forwards /graphql. Only used in dev.
  const backendOrigin =
    process.env.FRONTEND_BACKEND_ORIGIN || 'http://127.0.0.1:8080';

  return {
    mode: isProd ? 'production' : 'development',
    devtool: isProd ? 'source-map' : 'eval-cheap-module-source-map',
    entry: path.resolve(__dirname, 'src/index.tsx'),
    output: {
      path: path.resolve(__dirname, 'dist'),
      filename: isProd ? 'assets/[name].[contenthash:8].js' : 'assets/[name].js',
      chunkFilename: isProd
        ? 'assets/[name].[contenthash:8].chunk.js'
        : 'assets/[name].chunk.js',
      publicPath: '/',
      clean: true,
    },
    resolve: {
      extensions: ['.tsx', '.ts', '.jsx', '.js'],
      alias: {
        '@': path.resolve(__dirname, 'src'),
      },
    },
    module: {
      rules: [
        {
          test: /\.[tj]sx?$/,
          exclude: /node_modules/,
          use: {
            loader: 'swc-loader',
            options: {
              jsc: {
                parser: {
                  syntax: 'typescript',
                  tsx: true,
                  decorators: false,
                  dynamicImport: true,
                },
                transform: {
                  react: {
                    runtime: 'automatic',
                    development: !isProd,
                  },
                },
                target: 'es2020',
              },
            },
          },
        },
        {
          test: /\.(scss|sass|css)$/i,
          use: [
            'style-loader',
            {
              loader: 'css-loader',
              options: { importLoaders: 1, sourceMap: !isProd },
            },
            { loader: 'sass-loader', options: { sourceMap: !isProd } },
          ],
        },
        {
          test: /\.(png|jpe?g|gif|svg|webp)$/i,
          type: 'asset/resource',
          generator: { filename: 'assets/images/[name].[hash:8][ext]' },
        },
        {
          test: /\.(woff2?|ttf|otf|eot)$/i,
          type: 'asset/resource',
          generator: { filename: 'assets/fonts/[name].[hash:8][ext]' },
        },
      ],
    },
    plugins: [
      new HtmlWebpackPlugin({
        template: path.resolve(__dirname, 'public/index.html'),
        inject: 'body',
        minify: isProd ? { collapseWhitespace: true, removeComments: true } : false,
      }),
      new webpack.DefinePlugin({
        'process.env.NODE_ENV': JSON.stringify(isProd ? 'production' : 'development'),
        'process.env.GRAPHQL_ENDPOINT': JSON.stringify(graphqlEndpoint),
      }),
    ],
    devServer: {
      port: Number(process.env.FRONTEND_PORT) || 3000,
      hot: true,
      historyApiFallback: true,
      static: { directory: path.resolve(__dirname, 'public') },
      client: { overlay: { errors: true, warnings: false } },
      proxy: [
        {
          context: ['/graphql'],
          target: backendOrigin,
          changeOrigin: true,
          secure: false,
        },
      ],
    },
    performance: { hints: false },
  };
};
