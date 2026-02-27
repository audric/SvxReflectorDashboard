if User.count.zero?
  User.create!(callsign: "ADMIN", password: "changeme", password_confirmation: "changeme", role: "admin")
  puts "Created default admin user: ADMIN / changeme"
end

# Standard CTCSS tones (EIA/TIA-603)
# 50 tones: 38 original + 12 extended non-standard tones
if CtcssTone.count.zero?
  tones = [
    { frequency: 67.0,  code: "XZ" },
    { frequency: 69.3,  code: "WZ" },
    { frequency: 71.9,  code: "XA" },
    { frequency: 74.4,  code: "WA" },
    { frequency: 77.0,  code: "XB" },
    { frequency: 79.7,  code: "WB" },
    { frequency: 82.5,  code: "YZ" },
    { frequency: 85.4,  code: "YA" },
    { frequency: 88.5,  code: "YB" },
    { frequency: 91.5,  code: "ZZ" },
    { frequency: 94.8,  code: "ZA" },
    { frequency: 97.4,  code: "ZB" },
    { frequency: 100.0, code: "1Z" },
    { frequency: 103.5, code: "1A" },
    { frequency: 107.2, code: "1B" },
    { frequency: 110.9, code: "2Z" },
    { frequency: 114.8, code: "2A" },
    { frequency: 118.8, code: "2B" },
    { frequency: 123.0, code: "3Z" },
    { frequency: 127.3, code: "3A" },
    { frequency: 131.8, code: "3B" },
    { frequency: 136.5, code: "4Z" },
    { frequency: 141.3, code: "4A" },
    { frequency: 146.2, code: "4B" },
    { frequency: 151.4, code: "5Z" },
    { frequency: 156.7, code: "5A" },
    { frequency: 159.8, code: "5B" },
    { frequency: 162.2, code: "6Z" },
    { frequency: 165.5, code: "6A" },
    { frequency: 167.9, code: "6B" },
    { frequency: 171.3, code: "7Z" },
    { frequency: 173.8, code: "7A" },
    { frequency: 177.3, code: "M1" },
    { frequency: 179.9, code: "8Z" },
    { frequency: 183.5, code: "M2" },
    { frequency: 186.2, code: "M3" },
    { frequency: 189.9, code: "M4" },
    { frequency: 192.8, code: "M5" },
    { frequency: 196.6, code: "M6" },
    { frequency: 199.5, code: "M7" },
    { frequency: 203.5, code: "8A" },
    { frequency: 206.5, code: "M8" },
    { frequency: 210.7, code: "9Z" },
    { frequency: 218.1, code: "9A" },
    { frequency: 225.7, code: "9B" },
    { frequency: 229.1, code: "0Z" },
    { frequency: 233.6, code: "0A" },
    { frequency: 241.8, code: "0B" },
    { frequency: 250.3, code: "A1" },
    { frequency: 254.1, code: "A2" },
  ]

  CtcssTone.insert_all(tones.map { |t| t.merge(created_at: Time.current, updated_at: Time.current) })
  puts "Seeded #{tones.size} standard CTCSS tones"
end
