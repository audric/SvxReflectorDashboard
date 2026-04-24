module MarkdownHelper
  ALLOWED_TAGS = %w[
    p br hr
    h1 h2 h3 h4 h5 h6
    strong em del code pre blockquote
    ul ol li
    a img
    table thead tbody tr th td
    input
  ].freeze

  ALLOWED_ATTRS = %w[href src alt title type checked disabled class].freeze

  def render_markdown(text)
    return "".html_safe if text.blank?

    html = Kramdown::Document.new(text.to_s, input: "GFM", hard_wrap: false, auto_ids: false).to_html
    sanitize(html, tags: ALLOWED_TAGS, attributes: ALLOWED_ATTRS)
  end
end
