if User.count.zero?
  admin = User.new(callsign: "ADM1N", password: "changeme", password_confirmation: "changeme", role: "admin", reflector_admin: true)
  admin.save!
  puts "Created default admin user: ADM1N / changeme"
end
