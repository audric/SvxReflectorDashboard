if ENV['SMTP_ADDRESS'].present?
  Rails.application.config.action_mailer.delivery_method = :smtp
  settings = {
    address: ENV['SMTP_ADDRESS'],
    port: ENV.fetch('SMTP_PORT', 587).to_i,
    enable_starttls_auto: true,
    domain: ENV.fetch('SMTP_DOMAIN', ENV['DOMAIN'])
  }
  if ENV['SMTP_USERNAME'].present?
    settings[:user_name] = ENV['SMTP_USERNAME']
    settings[:password] = ENV['SMTP_PASSWORD']
    settings[:authentication] = :plain
  end
  Rails.application.config.action_mailer.smtp_settings = settings
  Rails.application.config.action_mailer.raise_delivery_errors = true
end
