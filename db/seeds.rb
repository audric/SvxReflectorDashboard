if User.count.zero?
  User.create!(callsign: "ADMIN", password: "changeme", password_confirmation: "changeme", role: "admin")
  puts "Created default admin user: ADMIN / changeme"
end
