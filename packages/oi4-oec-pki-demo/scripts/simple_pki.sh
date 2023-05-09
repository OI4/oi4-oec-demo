#!/usr/bin/env bash

for ARGUMENT in "$@"
do
    KEY=$(echo "$ARGUMENT" | cut -f1 -d=)

    KEY_LENGTH=${#KEY}
    VALUE="${ARGUMENT:$KEY_LENGTH+1}"
    export "$KEY"="$VALUE"
done

WORK_DIR="${WORK_DIR:-./build/$OI4_SERVICE}"
PKI_DIR="${PKI_DIR:-./pki}"
PKI_ROOT_KEY="${PKI_ROOT_KEY:-root/oi4-oec-pki-demo-root-ca.key}"
PKI_ROOT_CERT="${PKI_ROOT_CERT:-root/oi4-oec-pki-demo-root-ca.pem}"
PKI_ROOT_SERIAL="${PKI_ROOT_SERIAL:-root/oi4-oec-pki-demo-root-ca.srl}"
PKI_ROOT_CRL="${PKI_SUB_SERIAL:-root/oi4-oec-pki-demo-root-ca.crl}"
PKI_SUB_KEY="${PKI_SUB_KEY:-ca1/oi4-oec-pki-demo-sub-ca.key}"
PKI_SUB_CERT="${PKI_SUB_CERT:-ca1/oi4-oec-pki-demo-sub-ca.pem}"
PKI_SUB_SERIAL="${PKI_SUB_SERIAL:-ca1/oi4-oec-pki-demo-sub-ca.srl}"
PKI_SUB_CRL="${PKI_SUB_SERIAL:-ca1/oi4-oec-pki-demo-sub-ca.crl}"

OPERATION="${1:-issue_certificate}"

echo "Executing $OPERATION"

createKey(){
  KEY_LENGTH=${2:-2048}
  openssl genrsa -out "$1" "$KEY_LENGTH"
  echo "Key $1 created with key length $KEY_LENGTH"
}

createRootCa(){
  echo "Creating root CA"
  PKI_ROOT="$WORK_DIR/pki"
  mkdir -p "$PKI_ROOT"
  CA_KEY="$PKI_ROOT/$OI4_SERVICE-root-ca.key"

  createKey "$CA_KEY"

  CERT="$PKI_ROOT/$OI4_SERVICE-root-ca.pem"
  openssl req -new -x509 -days "$VALIDITY" -key "$CA_KEY" -out "$CERT" \
  -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME" \
  -addext "basicConstraints = CA:TRUE" \
  -addext "keyUsage = digitalSignature,keyCertSign,cRLSign" \

  printf "CA certificate %s issued with:\\n" "$CERT"
  printf "Country=%s\\nState=%s\\nLocality name=%s\\nOrganization=%s\\nOrganizational unit=%s\\nCommon name=%s" "$COUNTRY" "$STATE" "$LOCALITY_NAME" "$ORGANIZATION" "$ORGANIZATIONAL_UNIT", "$COMMON_NAME"
}

issueSubCertificate(){
  echo "Issuing sub CA"
  PKI_ROOT="$WORK_DIR/pki"
  mkdir -p "$PKI_ROOT"
  KEY="$PKI_ROOT/$OI4_SERVICE-sub-ca.key"

  createKey "$KEY"

  CSR="$PKI_ROOT/$OI4_SERVICE-sub-ca.csr"

  openssl req -new -key "$KEY" -out "$CSR" \
  -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME"

  CERT="$PKI_ROOT/$OI4_SERVICE-sub-ca.pem"
  openssl x509 -req -in "$CSR" -CA "$PKI_DIR/$PKI_ROOT_CERT" -CAkey "$PKI_DIR/$PKI_ROOT_KEY" -out "$CERT" -days "$VALIDITY" -sha256 \
  -extfile ./config/certificate_extensions.cfg -extensions sub_ca \
  -CAserial "$PKI_DIR/$PKI_ROOT_SERIAL" -CAcreateserial

  printf "Certificate %s issued with:\\n" "$CERT"
  printf "Country=%s\\nState=%s\\nLocality name=%s\\nOrganization=%s\\nOrganizational unit=%s\\nCommon name=%s" "$COUNTRY" "$STATE" "$LOCALITY_NAME" "$ORGANIZATION" "$ORGANIZATIONAL_UNIT", "$COMMON_NAME"
}

issueCertificate(){
  echo "Issuing certificate"
  mkdir -p "$WORK_DIR"
  KEY="$WORK_DIR/$OI4_SERVICE.key"

  createKey "$KEY"

  CSR="$WORK_DIR/$OI4_SERVICE.csr"

  openssl req -new -key "$KEY" -out "$CSR" \
  -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY_NAME/O=$ORGANIZATION/OU=$ORGANIZATIONAL_UNIT/CN=$COMMON_NAME"

  CERT="$WORK_DIR/$OI4_SERVICE.pem"
  openssl x509 -req -in "$CSR" -CA "$PKI_DIR/$PKI_SUB_CERT" -CAkey "$PKI_DIR/$PKI_SUB_KEY" -out "$CERT" -days "$VALIDITY" -sha256 \
  -extfile ./config/certificate_extensions.cfg -extensions client_certificate \
  -CAserial "$PKI_DIR/$PKI_SUB_SERIAL" -CAcreateserial

  printf "Certificate %s issued with:\\n" "$CERT"
  printf "Country=%s\\nState=%s\\nLocality name=%s\\nOrganization=%s\\nOrganizational unit=%s\\nCommon name=%s" "$COUNTRY" "$STATE" "$LOCALITY_NAME" "$ORGANIZATION" "$ORGANIZATIONAL_UNIT", "$COMMON_NAME"
}

revokeSubCa(){
  CERT="$PKI_DIR/$PKI_SUB_CERT"
  openssl ca -config ./config/certificate_configuration.cfg -name root_revoke -revoke "$CERT"
  printf "Certificate %s revoked\\n" "$CERT"

  CRL="$PKI_DIR/$PKI_ROOT_CRL"
  openssl ca -config ./config/certificate_configuration.cfg -name root_revoke -gencrl -out "$CRL"
  printf "CRL %s created/updated\\n" "CRL"
}

revokeCertificate(){
    CERT="$1"
    openssl ca -config ./config/certificate_configuration.cfg -name sub_ca_revoke -revoke "$CERT"
    printf "Certificate %s revoked\\n" "$CERT"

    CRL="$PKI_DIR/$PKI_SUB_CRL"
    openssl ca -config ./config/certificate_configuration.cfg -name sub_ca_revoke -gencrl -out "$CRL"
    printf "CRL %s created/updated\\n" "CRL"
}


if [ "$OPERATION" == "create_root_ca" ];then
    VALIDITY="${VALIDITY:-3650}"
    createRootCa
elif [ "$OPERATION" == "issue_sub_ca" ];then
    VALIDITY="${VALIDITY:-1825}"
    issueSubCertificate
elif [ "$OPERATION" == "revoke_sub_ca" ];then
    revokeSubCa
elif [ "$OPERATION" == "revoke_certificate" ];then
    revokeCertificate "$2"
else
    VALIDITY="${VALIDITY:-730}"
    issueCertificate
fi
