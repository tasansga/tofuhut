// Minimal dnscontrol example.
//
// Replace provider and credentials before running against a real zone.
//
// docs: https://docs.dnscontrol.org/

var REG_NONE = NewRegistrar("none");
var DNS_NONE = NewDnsProvider("none");

D("example.com", REG_NONE, DnsProvider(DNS_NONE),
  A("@", "127.0.0.1")
);
