FROM node:20-slim

WORKDIR /app

COPY package.json ./
RUN npm install --omit=dev

COPY instrumentation.js index.js ./

EXPOSE 8080

CMD ["npm", "start"]
