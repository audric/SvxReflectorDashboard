# Rate limiting for /status endpoint.
#
# Three tiers configured via Settings (admin UI):
#   - Blacklisted IPs/networks: blocked entirely (403)
#   - Trusted IPs/networks (trunk peers + manually added): rate_limit_trusted_rate (default 1s)
#   - Everyone else: rate_limit_public_rate (default 10s)
#
# IPs and CIDR networks supported (e.g. 192.168.0.0/24, 10.0.0.0/8).

class Rack::Attack
  Rack::Attack.cache.store = ActiveSupport::Cache::MemoryStore.new

  # Parse a comma-separated list of IPs/CIDRs into IPAddr objects
  def self.parse_ip_list(csv)
    (csv || '').split(',').filter_map do |entry|
      entry = entry.strip
      next if entry.blank?
      begin
        IPAddr.new(entry)
      rescue IPAddr::InvalidAddressError
        # Try resolving as hostname
        require 'resolv'
        Resolv.getaddresses(entry).filter_map { |a| IPAddr.new(a) rescue nil }
      end
    end.flatten
  end

  # Check if an IP matches any entry in a list of IPAddr objects
  def self.ip_in_list?(ip, list)
    addr = IPAddr.new(ip) rescue nil
    return false unless addr
    list.any? { |net| net.include?(addr) }
  end

  # ── Cached lookups (refreshed every 60s) ──────────────────────────────

  def self.blacklisted_nets
    refresh_lists_if_stale
    @blacklisted_nets || []
  end

  def self.trusted_nets
    refresh_lists_if_stale
    @trusted_nets || []
  end

  def self.refresh_lists_if_stale
    @lists_at ||= Time.at(0)
    return if (Time.now - @lists_at) < 60

    @blacklisted_nets = parse_ip_list(Setting.get('rate_limit_blacklist', ''))

    trusted = parse_ip_list(Setting.get('rate_limit_trusted_ips', ''))
    # Auto-include trunk peer hosts
    begin
      ReflectorConfig.load.trunks.each do |_name, trunk|
        next unless trunk['HOST'].present?
        trusted.concat(parse_ip_list(trunk['HOST']))
      end
    rescue
    end
    @trusted_nets = trusted

    @lists_at = Time.now
  end

  def self.trusted_period
    Setting.get('rate_limit_trusted_rate', '1').to_i.clamp(1, 60)
  end

  def self.public_period
    Setting.get('rate_limit_public_rate', '10').to_i.clamp(1, 300)
  end

  # ── Blacklist: block entirely ─────────────────────────────────────────

  blocklist('status/blacklist') do |req|
    req.path == '/status' && ip_in_list?(req.ip, blacklisted_nets)
  end

  # ── Throttles ─────────────────────────────────────────────────────────

  throttle('status/trusted', limit: proc { 1 }, period: proc { trusted_period }) do |req|
    req.ip if req.path == '/status' && ip_in_list?(req.ip, trusted_nets)
  end

  throttle('status/public', limit: proc { 1 }, period: proc { public_period }) do |req|
    req.ip if req.path == '/status' && !ip_in_list?(req.ip, trusted_nets) && !ip_in_list?(req.ip, blacklisted_nets)
  end

  # ── Responses ─────────────────────────────────────────────────────────

  self.blocklisted_responder = lambda do |_request|
    [403, { 'Content-Type' => 'application/json' }, [{ error: 'Forbidden' }.to_json]]
  end

  self.throttled_responder = lambda do |request|
    match_data = request.env['rack.attack.match_data'] || {}
    period = match_data[:period] || 10
    retry_after = period - (Time.now.to_i % period)
    [429, { 'Content-Type' => 'application/json', 'Retry-After' => retry_after.to_s },
     [{ error: 'Rate limit exceeded', retry_after: retry_after }.to_json]]
  end
end
