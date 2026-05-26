module DashboardHelper
  # Render a trunk peer's host as one or more links. HA-pair (twin) peers store
  # their two hostnames comma-joined (e.g. "a.example,b.example"); split them so
  # each becomes its own working link instead of one broken combined URL.
  def trunk_host_links(host, link_class: "text-gray-300")
    hosts = host.to_s.split(",").map(&:strip).reject(&:blank?)
    safe_join(
      hosts.map { |h|
        link_to(h, "https://#{h}", target: "_blank", rel: "noopener",
                class: "#{link_class} hover:text-blue-400 transition-colors")
      },
      content_tag(:span, ", ", class: "text-ghdim")
    )
  end
end
