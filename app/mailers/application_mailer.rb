class ApplicationMailer < ActionMailer::Base
  default from: -> { ENV.fetch('SMTP_FROM', "noreply@#{ENV.fetch('DOMAIN', 'example.com')}") }
  layout "mailer"
end
