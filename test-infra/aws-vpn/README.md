# aws-vpn infrastructure example

This example will provision a VPC on AWS, together with an EKS cluster that's private to the VPC and a ClientVPN endpoint to access it.
This is basically all that is needed for a private EKS cluster inside a VPC, and can be used to test how telepresence interacts with different VPN scenarios.

## How to use it

### 0. Prerequisites

You will need a route53 zone in your AWS account.
A hosted zone will be created as a subdomain of this existing zone to serve as the DNS name for the VPN's certificates.

### 1. Generating PKI

First, you need to generate key material for the VPN.
This can be done by simply running the `pki.sh` script in the `aws-vpn` directory.
The certs and keys for the VPN will be placed in a `certs` folder

### 2. Configuration

Next, you need to configure this terraform stack to generate a VPC/VPN/Cluster with the parameters you need.
The easiest way to do this is to create a `terraform.tfvars` file inside the `aws-vpn` directory and place the configuration's variables there.
The format of this file should be:


```hcl
aws_region              = "us-east-1" # The AWS region to use
parent_domain           = "foo.net" # The hosted zone mentioned in section 0
child_subdomain         = "my-subdomain" # The name of the subdomain that will be created under it.
child_subdomain_comment = "My subdomain's comment" # A human-readable description for the subdomain
vpc_cidr                = "10.0.0.0/16" # The CIDR range for IP addresses within the VPC
vpn_client_cidr         = "10.20.0.0/22" # The CIDR range for clients that connect to the VPN
service_cidr            = "10.19.0.0/16" # The CIDR range for k8s services in the EKS cluster
split_tunnel            = true # Whether the VPN should be configured with split tunneling
```

### 3. Deploying


