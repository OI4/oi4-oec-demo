# OI4 Open Edge Computing Demo PKI

| :exclamation:  This PKI is for demo and evaluation purposes only! Do not use is any (productive) product! |
|-----------------------------------------------------------------------------------------------------------|

[tl:dr](#headerUsage)

Security is one of the major concerns of the OI4 alliance. Securing connections, validating server and services is vital for IIoT.
Certificates are widely used for authentication, authorization and encryption. 
To handle a variety of certificates Public Key Infrastructures (PKI) are used. PKIs handle the whole lifecycle of certificates, from issuing to revocation.

A central part of PKIs are Certificate Authorities (CA). A CA issues certificates and adds a reference of its self to the certificate.
Certificates issued by a CA can be validated with the help of the issuing CA. Furthermore, a CA can revoke certificates, e.g. when the key gets compromised.

A PKI brings a lot of benefits but also adds additional complexity in the handling.

In typical real life IIoT projects, there is (or shall be) a PKI already available. But for testing and evaluation purposes these CAs and PKI like functions can be used.

## How it works

The whole functionality of this 'PKI' is based on [OpenSSL](https://www.openssl.org) and Bash.
The pre-generated CAs and the broker certificate can be used without any tooling. 
To issue new or revoke existing certificates or to set up CAs you can either use the bash script or use OpenSSL directly.
The OpenSSL commands are explained here as well

### PKI architecture

Okay, calling it an architecture is maybe a bit exaggerated...nevertheless, this is the basic structure and set up.

By the default the PKI consists of two CA, a root (root) and subordinate (ca1) CA. The root CA issues certificates for subordinate CAs (short sub CA).
On the other hand, sub CAs issue certificates for services, devices, etc. The two layers give a more fine-grained control over which certificates to trust and help with revocations.

Every CA has its own certificate revocation list (CRL), meaning the list of certificates there are no longer valid.

The root CA, sub CAs and the entity certificates do have different x509 usage declaration:

#### Root CA
basicConstraints = critical, CA:TRUE
keyUsage = critical, digitalSignature, keyCertSign, cRLSign

#### Sub CA
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, digitalSignature, keyCertSign, cRLSign

#### Entity certificate
basicConstraints = CA:FALSE
keyUsage = critical, nonRepudiation, digitalSignature, keyEncipherment, keyAgreement
extendedKeyUsage = critical, serverAuth, clientAuth

For details about the individual usage types, please see the x509 specification. Basically root and sub CA can be exclusively used for certificate signing and revocation, whereas entity certificates can be used for client and server authentication.

## <a name="headerUsage"></a>Usage

All operations are covered in the `scripts/simple_pki.sh` bash script and available as yarn scripts.

    "issueCertificate": "./scripts/simple_pki.sh issue_certificate",
    "revokeCertificate": "./scripts/simple_pki.sh revoke_certificate",
    "issueSubCA": "./scripts/simple_pki.sh issue_sub_ca",
    "revokeSubCA": "./scripts/simple_pki.sh revoke_sub_ca",
    "issueRootCA": "./scripts/simple_pki.sh create_root_ca "
