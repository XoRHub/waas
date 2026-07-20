FROM --platform=$BUILDPLATFORM node:24.18.0-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.31-alpine@sha256:18d67281256ded39ff65e010ae4f831be18f19356f83c60bc546492c7eb6dd23
# Pull Alpine security fixes published after the upstream image build
# (the trivy image gate blocks on fixed HIGH/CRITICAL OS packages).
USER root
RUN apk --no-cache upgrade
USER 101
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 8080
