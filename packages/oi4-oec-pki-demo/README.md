# OI4 Open Edge Computing Demo PKI

| :exclamation: This PKI is for demo and evaluation purposes only! Do not use is any (productive) environment :exclamation: |
|---------------------------------------------------------------------------------------------------------------------------|

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
The OpenSSL commands are explained [here](#headerOpenSSLCommands) as well

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

All operations are covered in the `scripts/simple_pki.sh` bash script and available as yarn scripts:
 - Issue entity certificate: `yarn run issueCertificate`
 - Revoke entity certificate: `yarn run revokeCertificate <path to the certificate>`
 - Issue subordinate CA: `yarn run issueSubCA`
 - Revoke subordinate CA: `yarn run revokeSubCA`
 - Create root CA: `yarn run createRootCA`

### Configuration

The subject (CN, OU, etc.) of the certificate can be set with the following environment variables:
C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME"
- `OI4_SERVICE` used for the file naming
- `COUNTRY` subject C in the certificate
- `STATE` subject ST in the certificate
- `LOCALITY_NAME` subject L in the certificate
- `ORGANIZATION` subject O in the certificate
- `ORGANIZATIONAL_UNIT` subject OU in the certificate
- `COMMON_NAME` subject CN in the certificate

Besides of the environment variables the script offers a couple of parameters that can be added to the call with `key=value`.
E.g. to issue a certificate with a validity of 1 year: `yarn run issueCertificate VALIDITY=365`
The usage of the parameters depend on the actual operation.

Finally, there is the OpenSSL configuration in [config/certificate_configuration.cfg](config%2Fcertificate_configuration.cfg) and the extensions in  [config/certificate_extensions.cfg](config%2Fcertificate_extensions.cfg)

### Issue entity certificate

Command: `yarn run issueCertificate`

Parameters:

| Key              | Description                                | Default                           |
|------------------|--------------------------------------------|-----------------------------------|
| WORK_DIR         | Output directory                           | ./build/$OI4_SERVICE              |
| PKI_DIR          | CA base folder                             | ./pki                             |
| PKI_SUB_KEY      | Private key file of the sub CA             | ca1/oi4-oec-pki-demo-sub-ca.key   |
| PKI_SUB_CERT     | Certificate file of the sub CA             | ca1/oi4-oec-pki-demo-sub-ca.pem   |
| PKI_SUB_SERIAL   | Serial file of the root CA                 | ca1/oi4-oec-pki-demo-sub-ca.srl   |
| VALIDITY         | Validity of the issued certificate in days | 730 (days)                        |

By default, an entity certificate with a validity of two years is generated with subordinate CA `ca1` located in the `pki` folder

### Revoke entity certificate

Command: `yarn run revokeCertificate <path to the certificate>`

Parameters:

| Key              | Description                             | Default                         |
|------------------|-----------------------------------------|---------------------------------|
| PKI_DIR          | CA base folder                          | ./pki                           |
| PKI_SUB_CRL      | Sub CA certificate revocation list file | ca1/oi4-oec-pki-demo-sub-ca.crl |

The certificate addressed in the command will be revoked from the according subordinate CA. 
It is important, that the certificate has been issued by this CA, so that it can be revoked.
The CA and revocation is configured in the `sub_ca_revoke` block of the OpenSSL configuration [cert/certificate_configuration.cfg](config%2Fcertificate_configuration.cfg)

### Issue subordinate CA

Command: `yarn run issueSubCA`

Parameters:

| Key              | Description                                | Default                           |
|------------------|--------------------------------------------|-----------------------------------|
| WORK_DIR         | Output directory                           | ./build/$OI4_SERVICE/pki          |
| PKI_DIR          | CA base folder                             | ./pki                             |
| PKI_ROOT_KEY     | Private key file of the root CA            | root/oi4-oec-pki-demo-root-ca.key |
| PKI_ROOT_CERT    | Certificate file of the root CA            | root/oi4-oec-pki-demo-root-ca.pem |
| PKI_ROOT_SERIAL  | Serial file of the root CA                 | root/oi4-oec-pki-demo-root-ca.srl |
| VALIDITY         | Validity of the issued certificate in days | 1825 (days)                       |

The command will derive a new subordinate CA from the root CA. By default, the CA has a validity of 5 years.

### Revoke subordinate CA

Command: `yarn run revokeSubCA`

Parameters:

| Key           | Description                              | Default                           |
|---------------|------------------------------------------|-----------------------------------|
| PKI_DIR       | CA base folder                           | ./pki                             |
| PKI_SUB_CERT  | Certificate file of the sub CA           | ca1/oi4-oec-pki-demo-sub-ca.pem   |
| PKI_ROOT_CRL  | Root CA certificate revocation list file | root/oi4-oec-pki-demo-root-ca.crl |

Revokes the subordinate CA addressed in the parameter `PKI_SUB_CERT`.
It is important, that the sub CA certificate has been issued by this root CA, so that it can be revoked.
The root CA and revocation is configured in the `root_revoke` block of the OpenSSL configuration [cert/certificate_configuration.cfg](config%2Fcertificate_configuration.cfg)

### Create root CA

Command: `yarn run createRootCA`

Parameters:

| Key              | Description                                | Default                           |
|------------------|--------------------------------------------|-----------------------------------|
| WORK_DIR         | Output directory                           | ./build/$OI4_SERVICE              |
| VALIDITY         | Validity of the issued certificate in days | 3650 (days)                       |

This command is going to issue a self-signed certificate to be used as root CA with a validity of 10 years.

## <a name="headerOpenSSLCommands"></a>OpenSSL commands

OpenSSL covers all the needed functionality to generate keys, issue and revoke certificate. 
OpenSSL defines many operations that are partially overlapping. Beside of the commands the OpenSSL takes some settings out of the [configuration](config%2Fcertificate_configuration.cfg) and [extensions](config%2Fcertificate_extensions.cfg) files.
Please find the explanation of those files in the OpenSSL documentation.

The key generation for the individual certificates and signing requests (CSR) is always the same.

### Generate private key

`openssl genrsa -out <file_name> 2048`

### Create root CA

As a root CA has no parent CA by definition, the root CA is a self-signed certificated with specific basic constraints and key usages: 

```
openssl req -new -x509 -days 3650 -key <key_file> -out <certificate_output_file> \
-subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME" \
-addext "basicConstraints = CA:TRUE" \
-addext "keyUsage = digitalSignature,keyCertSign,cRLSign"
```

### Issue subordinate CA

A subordinate CA is basically a regular certificate, with special basic constraints, key and extended key usages.
Contrary to the root CA, a sub CA has a least one parent CA. Therefore, a certificate sign request is generated and based on this the parent CA issues an according certificate
See the [certificate_extensions.cfg](config%2Fcertificate_extensions.cfg) for details.

```
openssl req -new -key <key_file> -out <csr_output_file> \
-subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME"
```

```
openssl x509 -req -in  <csr_file> -CA  <parent_ca_certificate_file> -CAkey <parent_ca_key_file> -out <ca_certificate_output_file> -days 1825 -sha256 \
  -extfile ./config/certificate_extensions.cfg -extensions sub_ca \
  -CAserial <parent_ca_serial_file> -CAcreateserial
```

### Issue entity certificate

An entity certificate is issued like a subordinate CA, but with different extensions, that do not allow to issue certificates but authentication.
See the [certificate_extensions.cfg](config%2Fcertificate_extensions.cfg) for details.

```
openssl req -new -key <key_file> -out <csr_output_file> \
-subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME"
```

```
openssl x509 -req -in  <csr_file> -CA  <ca_certificate_file> -CAkey <ca_key_file> -out <certificate_output_file> -days 1825 -sha256 \
  -extfile ./config/certificate_extensions.cfg -extensions client_certificate \
  -CAserial <ca_serial_file> -CAcreateserial
```

### Revoke certificates

CAs can revoke certificates that they have issued. The revocations are listed in CRL (Certificate Revocation List) files.
The CSR are signed with by the CA, and therefore it can be validated.
To enable the revocation of certificates, the CA must track the issuing (serial files) and revocation. 
Please take a look at the [certificate_configuration.cfg](config%2Fcertificate_configuration.cfg) for details. 

In a first step, the certificates are revoked...

```
openssl ca -config ./config/certificate_configuration.cfg -name <config_name, e.g. sub_ca_revoke> -revoke <certificate_file_to_be_revoked>
```

...and in a second step the  CRL must be created:

```
openssl ca -config ./config/certificate_configuration.cfg -name <config_name, e.g. sub_ca_revoke> -gencrl -out <CRL_output_file>
```
