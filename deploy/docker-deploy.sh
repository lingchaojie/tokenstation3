#!/bin/bash
# =============================================================================
# Sub2API Docker Deployment Preparation Script
# =============================================================================
# This script prepares deployment files for Sub2API:
#   - Downloads docker-compose.local.yml and .env.example
#   - Generates secure secrets (JWT_SECRET, TOTP_ENCRYPTION_KEY, POSTGRES_PASSWORD)
#   - Creates necessary data directories
#
# After running this script, you can start services with:
#   docker compose up -d
# =============================================================================

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# GitHub raw content base URL
GITHUB_RAW_URL="${GITHUB_RAW_URL:-https://raw.githubusercontent.com/lingchaojie/tokenstation3/release/deploy}"
CUSTOM_DOMAIN_CONFIGURED=0
CONFIGURED_PRIMARY_DOMAIN=""
CONFIGURED_APEX_DOMAIN=""
CONFIGURED_ADDITIONAL_DOMAINS=""

# Print colored message
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Generate random secret
generate_secret() {
    openssl rand -hex 32
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Return the Docker Compose command users should run after this script exits.
docker_compose_command() {
    if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
        printf 'sudo docker compose'
    else
        printf 'docker compose'
    fi
}

# Validate a DNS hostname used for Caddy site addresses.
validate_domain_name() {
    local name="$1"

    if [ -z "$name" ]; then
        return 1
    fi

    if [ "${#name}" -gt 253 ]; then
        return 1
    fi

    case "$name" in
        *://*|*/*|*:*)
            return 1
            ;;
    esac

    printf '%s' "$name" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$'
}

normalize_domain_name() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

# Print validated additional domains, one normalized hostname per line.
normalize_additional_domains() {
    local raw="$1"
    local primary_domain=""
    local apex_domain=""
    local domain=""
    local normalized_domain=""
    local -a domains=()
    local -A seen_domains=()

    primary_domain="$(normalize_domain_name "$2")"
    apex_domain="$(normalize_domain_name "$3")"
    IFS=$' \t\r\n,' read -r -d '' -a domains < <(printf '%s\0' "$raw")

    for domain in "${domains[@]}"; do
        normalized_domain="$(normalize_domain_name "$domain")"

        if ! validate_domain_name "$normalized_domain"; then
            return 1
        fi
        if [ "$normalized_domain" = "$primary_domain" ] || \
            { [ -n "$apex_domain" ] && [ "$normalized_domain" = "$apex_domain" ]; }; then
            return 1
        fi
        if [ -n "${seen_domains[$normalized_domain]+set}" ]; then
            return 1
        fi

        seen_domains["$normalized_domain"]=1
        printf '%s\n' "$normalized_domain"
    done
}

# Validate requested Caddy domains before deployment preparation mutates files.
preflight_requested_domains() {
    local domain="${DOMAIN:-}"
    local apex_domain="${APEX_DOMAIN:-}"
    local additional_domains=""

    CUSTOM_DOMAIN_CONFIGURED=0
    CONFIGURED_PRIMARY_DOMAIN=""
    CONFIGURED_APEX_DOMAIN=""
    CONFIGURED_ADDITIONAL_DOMAINS=""

    if [ -z "$domain" ]; then
        return 0
    fi

    if ! validate_domain_name "$domain"; then
        print_error "Invalid DOMAIN: ${domain}"
        print_error "Use a hostname such as www.example.com, without https:// or paths."
        return 1
    fi

    if [ -n "$apex_domain" ] && ! validate_domain_name "$apex_domain"; then
        print_error "Invalid APEX_DOMAIN: ${apex_domain}"
        print_error "Use a hostname such as example.com, without https:// or paths."
        return 1
    fi

    domain="$(normalize_domain_name "$domain")"
    apex_domain="$(normalize_domain_name "$apex_domain")"

    if [ -n "$apex_domain" ] && [ "$domain" = "$apex_domain" ]; then
        print_error "DOMAIN and APEX_DOMAIN must be different hostnames."
        print_error "Hostname comparisons are case-insensitive."
        return 1
    fi

    if ! additional_domains="$(normalize_additional_domains "${ADDITIONAL_DOMAINS:-}" "$domain" "$apex_domain")"; then
        print_error "Invalid ADDITIONAL_DOMAINS."
        print_error "Use unique hostnames separated by commas or whitespace, without https:// or paths."
        return 1
    fi

    CONFIGURED_PRIMARY_DOMAIN="$domain"
    CONFIGURED_APEX_DOMAIN="$apex_domain"
    CONFIGURED_ADDITIONAL_DOMAINS="$additional_domains"
}

# Render the complete managed Caddy config without changing the filesystem.
render_managed_caddyfile() {
    local domain="$1"
    local apex_domain="$2"
    local upstream_port="$3"
    local additional_domains="${4:-}"
    local additional_domain=""

    printf '%s\n' \
        '# Managed by Sub2API/TokenStation docker-deploy.sh.' \
        '# To change domains after deployment, edit this file and reload Caddy.'

    if [ -n "$apex_domain" ]; then
        printf '%s {\n\tredir https://%s{uri} permanent\n}\n\n' "$apex_domain" "$domain"
    fi

    printf '%s {\n\treverse_proxy 127.0.0.1:%s\n}\n' "$domain" "$upstream_port"

    while IFS= read -r additional_domain || [ -n "$additional_domain" ]; do
        if [ -z "$additional_domain" ]; then
            continue
        fi
        printf '\n%s {\n\treverse_proxy 127.0.0.1:%s\n}\n' "$additional_domain" "$upstream_port"
    done <<< "$additional_domains"
}

# Validate a TCP port without triggering Bash integer errors.
validate_port() {
    local port="$1"

    if ! printf '%s' "$port" | grep -Eq '^[0-9]{1,5}$'; then
        return 1
    fi

    [ "$port" -ge 1 ] && [ "$port" -le 65535 ]
}

# Return the host port that maps to container port 8080.
get_public_server_port() {
    if [ -f .env ] && grep -Eq '^SERVER_PORT=' .env; then
        grep -E '^SERVER_PORT=' .env | tail -n 1 | cut -d '=' -f 2-
    else
        printf '8080'
    fi
}

# Set or append a KEY=value pair in .env.
set_env_value() {
    local key="$1"
    local value="$2"

    if grep -Eq "^${key}=" .env; then
        if sed --version >/dev/null 2>&1; then
            sed -i "s/^${key}=.*/${key}=${value}/" .env
        else
            sed -i '' "s/^${key}=.*/${key}=${value}/" .env
        fi
    else
        printf '\n%s=%s\n' "$key" "$value" >> .env
    fi
}

# Persist SERVER_PORT environment override into .env so Docker and Caddy agree.
sync_server_port_env() {
    if [ -z "${SERVER_PORT:-}" ]; then
        return 0
    fi

    if ! validate_port "$SERVER_PORT"; then
        print_error "Invalid SERVER_PORT: ${SERVER_PORT}"
        print_error "Use a TCP port from 1 to 65535."
        exit 1
    fi

    set_env_value "SERVER_PORT" "$SERVER_PORT"
}

# In domain mode, bind the app port to loopback by default so traffic goes through Caddy.
sync_bind_host_env() {
    local bind_host="${BIND_HOST:-127.0.0.1}"

    if [ -z "${BIND_HOST:-}" ]; then
        print_info "DOMAIN is set; binding the Docker host port to 127.0.0.1 so public traffic goes through Caddy."
    else
        case "$bind_host" in
            127.0.0.1)
                ;;
            localhost|::1)
                print_info "Normalizing BIND_HOST=${bind_host} to Docker-compatible loopback address 127.0.0.1."
                bind_host="127.0.0.1"
                ;;
            *)
                print_warning "BIND_HOST=${bind_host} will expose the app port directly. Use a firewall if this is intentional."
                ;;
        esac
    fi

    set_env_value "BIND_HOST" "$bind_host"
}

# Install Caddy on Debian/Ubuntu when DOMAIN is provided and Caddy is missing.
install_caddy_if_needed() {
    if command_exists caddy; then
        print_success "Caddy is already installed: $(caddy version 2>/dev/null | head -n 1)"
        return 0
    fi

    if [ "$(id -u)" -ne 0 ]; then
        print_warning "DOMAIN is set, but Caddy is not installed and this script is not running as root."
        print_warning "Run the initial DOMAIN setup with sudo, or install and configure Caddy manually."
        return 1
    fi

    if ! command_exists apt-get; then
        print_warning "DOMAIN is set, but automatic Caddy installation currently supports apt-based systems only."
        print_warning "Install Caddy manually, then configure /etc/caddy/sub2api/sub2api.caddy and import it from /etc/caddy/Caddyfile."
        return 1
    fi

    print_info "Installing Caddy from the official repository..."
    apt-get update || {
        print_warning "Unable to update apt package lists for Caddy installation."
        return 1
    }
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg || {
        print_warning "Unable to install Caddy repository prerequisites."
        return 1
    }
    install -d -m 0755 /usr/share/keyrings || {
        print_warning "Unable to create Caddy keyring directory."
        return 1
    }
    curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg || {
        print_warning "Unable to install Caddy repository signing key."
        return 1
    }
    chmod 0644 /usr/share/keyrings/caddy-stable-archive-keyring.gpg || {
        print_warning "Unable to set permissions on the Caddy signing key."
        return 1
    }
    curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list || {
        print_warning "Unable to install the Caddy apt source list."
        return 1
    }
    chmod 0644 /etc/apt/sources.list.d/caddy-stable.list || {
        print_warning "Unable to set permissions on the Caddy apt source list."
        return 1
    }
    apt-get update || {
        print_warning "Unable to update apt package lists after adding the Caddy repository."
        return 1
    }
    apt-get install -y caddy || {
        print_warning "Unable to install Caddy."
        return 1
    }
    print_success "Installed Caddy: $(caddy version 2>/dev/null | head -n 1)"
}

restore_caddy_backup() {
    local file_path="$1"
    local backup_path="$2"
    local existed_before="$3"

    if [ "$existed_before" = "1" ]; then
        cp -a "$backup_path" "$file_path" || print_warning "Unable to restore ${file_path} from ${backup_path}."
    else
        rm -f "$file_path" || print_warning "Unable to remove newly created ${file_path}."
    fi
}

restore_caddy_backups() {
    local caddyfile="$1"
    local managed_caddyfile="$2"
    local caddyfile_backup="$3"
    local managed_caddyfile_backup="$4"
    local caddyfile_existed="$5"
    local managed_caddyfile_existed="$6"

    print_warning "Restoring previous Caddy configuration."
    restore_caddy_backup "$managed_caddyfile" "$managed_caddyfile_backup" "$managed_caddyfile_existed"
    restore_caddy_backup "$caddyfile" "$caddyfile_backup" "$caddyfile_existed"

    if command_exists systemctl && systemctl is-active --quiet caddy 2>/dev/null; then
        systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || \
            print_warning "Restored files, but Caddy did not reload cleanly. Check: systemctl status caddy"
    fi
}

# Write managed Caddy config without replacing unrelated sites.
write_caddyfile() {
    local domain="$1"
    local apex_domain="$2"
    local upstream_port="$3"
    local additional_domains="${4:-}"
    local caddyfile="${5:-/etc/caddy/Caddyfile}"
    local managed_caddyfile="${6:-/etc/caddy/sub2api/sub2api.caddy}"
    local managed_dir=""
    local import_line="import ${managed_caddyfile}"
    local timestamp=""
    local caddyfile_backup=""
    local managed_caddyfile_backup=""
    local caddyfile_existed="0"
    local managed_caddyfile_existed="0"

    managed_dir="$(dirname "$managed_caddyfile")"

    if [ "$(id -u)" -ne 0 ]; then
        print_warning "DOMAIN is set, but writing ${caddyfile} requires root privileges."
        print_warning "Run the initial DOMAIN setup with sudo or configure Caddy manually."
        return 1
    fi

    mkdir -p "$managed_dir" || {
        print_warning "Unable to create ${managed_dir}."
        return 1
    }

    timestamp="$(date -u +%Y%m%d%H%M%S)"

    if [ -f "$caddyfile" ]; then
        caddyfile_existed="1"
        caddyfile_backup="${caddyfile}.backup.${timestamp}"
        cp -a "$caddyfile" "$caddyfile_backup" || {
            print_warning "Unable to back up existing Caddyfile."
            return 1
        }
        print_info "Backed up existing Caddyfile to ${caddyfile_backup}"
    fi

    if [ -f "$managed_caddyfile" ]; then
        managed_caddyfile_existed="1"
        managed_caddyfile_backup="${managed_caddyfile}.backup.${timestamp}"
        cp -a "$managed_caddyfile" "$managed_caddyfile_backup" || {
            print_warning "Unable to back up existing managed Caddy config."
            return 1
        }
        print_info "Backed up existing managed Caddy config to ${managed_caddyfile_backup}"
    fi

    if ! render_managed_caddyfile "$domain" "$apex_domain" "$upstream_port" "$additional_domains" > "$managed_caddyfile"; then
        print_warning "Unable to write ${managed_caddyfile}."
        restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
        return 1
    fi

    if [ ! -f "$caddyfile" ]; then
        printf '%s\n' "$import_line" > "$caddyfile" || {
            print_warning "Unable to write ${caddyfile}."
            restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
            return 1
        }
    elif ! grep -Fxq "$import_line" "$caddyfile"; then
        if [ -s "$caddyfile" ]; then
            printf '\n%s\n' "$import_line" >> "$caddyfile" || {
                print_warning "Unable to add managed import to ${caddyfile}."
                restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
                return 1
            }
        else
            printf '%s\n' "$import_line" >> "$caddyfile" || {
                print_warning "Unable to add managed import to ${caddyfile}."
                restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
                return 1
            }
        fi
    fi

    caddy fmt --overwrite "$managed_caddyfile" || {
        print_warning "Unable to format ${managed_caddyfile}."
        restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
        return 1
    }
    caddy validate --config "$caddyfile" || {
        print_warning "Caddy validation failed for ${caddyfile}."
        restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
        return 1
    }
    systemctl enable --now caddy || {
        print_warning "Unable to enable and start Caddy."
        restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
        return 1
    }
    systemctl reload caddy || systemctl restart caddy || {
        print_warning "Unable to reload or restart Caddy."
        restore_caddy_backups "$caddyfile" "$managed_caddyfile" "$caddyfile_backup" "$managed_caddyfile_backup" "$caddyfile_existed" "$managed_caddyfile_existed"
        return 1
    }
    print_success "Configured Caddy for ${domain}"

    if [ -n "$apex_domain" ]; then
        print_success "Configured ${apex_domain} to redirect to https://${domain}"
    fi
}

# Configure Caddy only when DOMAIN is explicitly provided.
configure_caddy_if_requested() {
    local domain=""
    local apex_domain=""
    local additional_domains=""
    local additional_domain=""
    local upstream_port=""

    if ! preflight_requested_domains; then
        exit 1
    fi

    domain="$CONFIGURED_PRIMARY_DOMAIN"
    apex_domain="$CONFIGURED_APEX_DOMAIN"
    additional_domains="$CONFIGURED_ADDITIONAL_DOMAINS"

    if [ -z "$domain" ]; then
        return 0
    fi

    print_info "DOMAIN is set: ${domain}"

    sync_server_port_env
    sync_bind_host_env
    upstream_port="$(get_public_server_port)"

    if ! validate_port "$upstream_port"; then
        print_error "Invalid SERVER_PORT for Caddy upstream: ${upstream_port}"
        print_error "Use a TCP port from 1 to 65535."
        exit 1
    fi

    install_caddy_if_needed || {
        print_error "DOMAIN was provided, but Caddy could not be installed or found."
        print_error "Install Caddy manually, or run DOMAIN setup with sudo in a fresh deployment directory."
        exit 1
    }
    write_caddyfile "$domain" "$apex_domain" "$upstream_port" "$additional_domains" || {
        print_error "DOMAIN was provided, but Caddy configuration failed."
        print_error "Fix the warnings above or configure Caddy manually."
        exit 1
    }
    CUSTOM_DOMAIN_CONFIGURED=1

    echo ""
    print_info "Custom domain notes:"
    print_info "  - Point DNS for ${domain} to this server before expecting HTTPS to work."
    if [ -n "$apex_domain" ]; then
        print_info "  - Point DNS for ${apex_domain} to this server as well."
    fi
    while IFS= read -r additional_domain || [ -n "$additional_domain" ]; do
        if [ -n "$additional_domain" ]; then
            print_info "  - Point DNS for ${additional_domain} to this server as well."
        fi
    done <<< "$additional_domains"
    print_info "  - Caddy app config is stored in /etc/caddy/sub2api/sub2api.caddy and imported by /etc/caddy/Caddyfile."
    print_info "  - The app port is bound to BIND_HOST=$(grep -E '^BIND_HOST=' .env | tail -n 1 | cut -d '=' -f 2-) for this deployment."
    print_info "  - Future app updates only need: docker compose pull && docker compose up -d"
}

# Main installation function
main() {
    if ! preflight_requested_domains; then
        exit 1
    fi

    echo ""
    echo "=========================================="
    echo "  Sub2API Deployment Preparation"
    echo "=========================================="
    echo ""

    # Check if openssl is available
    if ! command_exists openssl; then
        print_error "openssl is not installed. Please install openssl first."
        exit 1
    fi

    # Check if deployment already exists
    if [ -f "docker-compose.yml" ] && [ -f ".env" ]; then
        print_warning "Deployment files already exist in current directory."
        read -p "Overwrite existing files? (y/N): " -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Cancelled."
            exit 0
        fi
    fi

    # Download docker-compose.local.yml and save as docker-compose.yml
    print_info "Downloading docker-compose.yml..."
    if command_exists curl; then
        curl -fsSL "${GITHUB_RAW_URL}/docker-compose.local.yml" -o docker-compose.yml
    elif command_exists wget; then
        wget -q "${GITHUB_RAW_URL}/docker-compose.local.yml" -O docker-compose.yml
    else
        print_error "Neither curl nor wget is installed. Please install one of them."
        exit 1
    fi
    if [ ! -s docker-compose.yml ]; then
        print_error "Downloaded docker-compose.yml is empty."
        exit 1
    fi
    print_success "Downloaded docker-compose.yml"

    # Download .env.example
    print_info "Downloading .env.example..."
    if command_exists curl; then
        curl -fsSL "${GITHUB_RAW_URL}/.env.example" -o .env.example
    else
        wget -q "${GITHUB_RAW_URL}/.env.example" -O .env.example
    fi
    if [ ! -s .env.example ]; then
        print_error "Downloaded .env.example is empty."
        exit 1
    fi
    print_success "Downloaded .env.example"

    # Generate .env file with auto-generated secrets
    print_info "Generating secure secrets..."
    echo ""

    # Generate secrets
    JWT_SECRET=$(generate_secret)
    TOTP_ENCRYPTION_KEY=$(generate_secret)
    POSTGRES_PASSWORD=$(generate_secret)

    # Create .env from .env.example
    cp .env.example .env

    # Update .env with generated secrets (cross-platform compatible)
    if sed --version >/dev/null 2>&1; then
        # GNU sed (Linux)
        sed -i "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
    else
        # BSD sed (macOS)
        sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i '' "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
    fi

    # Create data directories
    print_info "Creating data directories..."
    mkdir -p data postgres_data redis_data
    print_success "Created data directories"

    # Set secure permissions for .env file (readable/writable only by owner)
    chmod 600 .env
    echo ""

    # Optional custom domain configuration. This is intentionally opt-in so the
    # original one-click Docker deployment behavior remains unchanged.
    configure_caddy_if_requested
    echo ""

    # Display completion message
    local compose_cmd
    compose_cmd="$(docker_compose_command)"

    echo "=========================================="
    echo "  Preparation Complete!"
    echo "=========================================="
    echo ""
    echo "Generated secure credentials:"
    echo "  POSTGRES_PASSWORD:     ${POSTGRES_PASSWORD}"
    echo "  JWT_SECRET:            ${JWT_SECRET}"
    echo "  TOTP_ENCRYPTION_KEY:   ${TOTP_ENCRYPTION_KEY}"
    echo ""
    print_warning "These credentials have been saved to .env file."
    print_warning "Please keep them secure and do not share publicly!"
    echo ""
    echo "Directory structure:"
    echo "  docker-compose.yml        - Docker Compose configuration"
    echo "  .env                      - Environment variables (generated secrets)"
    echo "  .env.example              - Example template (for reference)"
    echo "  data/                     - Application data (will be created on first run)"
    echo "  postgres_data/            - PostgreSQL data"
    echo "  redis_data/               - Redis data"
    echo ""
    echo "Next steps:"
    echo "  1. (Optional) Edit .env to customize configuration"
    echo "  2. Start services:"
    echo "     ${compose_cmd} up -d"
    echo ""
    echo "  3. View logs:"
    echo "     ${compose_cmd} logs -f sub2api"
    echo ""
    echo "  4. Access Web UI:"
    echo "     http://localhost:8080"
    echo ""
    if [ "$CUSTOM_DOMAIN_CONFIGURED" = "1" ]; then
        local additional_domain=""

        echo "  Custom domain:"
        echo "     https://${CONFIGURED_PRIMARY_DOMAIN}"
        if [ -n "$CONFIGURED_APEX_DOMAIN" ]; then
            echo "     https://${CONFIGURED_APEX_DOMAIN} redirects to https://${CONFIGURED_PRIMARY_DOMAIN}"
        fi
        while IFS= read -r additional_domain || [ -n "$additional_domain" ]; do
            if [ -n "$additional_domain" ]; then
                echo "     https://${additional_domain}"
            fi
        done <<< "$CONFIGURED_ADDITIONAL_DOMAINS"
        echo ""
    else
        echo "  Optional custom domain: enable during initial preparation,"
        echo "     or follow deploy/README.md to configure Caddy manually."
        echo ""
    fi
    print_info "If admin password is not set in .env, it will be auto-generated."
    print_info "Check logs for the generated admin password on first startup."
    echo ""
}

# Run main function unless the script is being sourced for tests.
if [ "${DOCKER_DEPLOY_SOURCE_ONLY:-0}" != "1" ]; then
    main "$@"
fi
