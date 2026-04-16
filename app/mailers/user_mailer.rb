class UserMailer < ApplicationMailer
  def approved(user)
    @user = user
    @brand = Setting.get('brand_name', ENV.fetch('BRAND_NAME', 'SVXReflector'))
    @login_url = "https://#{ENV.fetch('DOMAIN', 'localhost')}/login"
    mail(to: user.email, subject: "#{@brand} — your account has been approved")
  end
end
