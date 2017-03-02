#!/usr/bin/python3

from json import loads
from os import setuid
from subprocess import Popen, STDOUT, check_output
from sys import argv



def _get_service_keys(environment):
    # XXX duplicated in remote-telepresence
    # XXX also check for TCPness.
    result = [key for key in environment if key.endswith("_SERVICE_HOST")]
    result.sort(key=lambda s: s[:-len("_SERVICE_HOST")])
    return result


def get_remote_env(pod_name):
    env = str(check_output(["kubectl", "exec", pod_name, "env"]), "utf-8")
    result = {}
    for line in env.splitlines():
        key, value = line.split("=", 1)
        result[key] = value
    return result


def get_env_variables(pod_name):
    """Generate environment variables that match kubernetes."""
    # XXX we're recreating the port generation logic
    i = 0
    for i, service_key in enumerate(_get_service_keys(get_remote_env(pod_name))):
        port = str(2000 + i)
        ip = "127.0.0.1"
        # XXX bad abstraction
        name = service_key[:-len("_SERVICE_HOST")]
        # XXX will be wrong for UDP
        full_address = "tcp://{}:{}".format(ip, port)
        yield name + "_SERVICE_HOST", ip
        yield name + "_SERVICE_PORT", port
        yield name + "_PORT", full_address
        port_name = name + "_PORT_" + port + "_TCP"
        yield port_name, full_address
        # XXX will break for UDP
        yield port_name + "_PROTO", "tcp"
        yield port_name + "_PORT", port
        yield port_name + "_HOST", ip


def write_env(pod_name):
    with open("/output/k8s.env", "w") as f:
        for key, value in get_env_variables(pod_name):
            f.write("{}={}\n".format(key, value))
    print("Please pass --env-file=k8s.env to docker run.")


def write_etc_hosts():
    """Update /etc/hosts with records that match k8s DNS entries for services."""
    services_json = loads(str(
        check_output(["kubectl", "get", "service", "-o", "json"]), "utf-8"))
    with open("/etc/hosts", "a") as hosts:
        for service in services_json["items"]:
            name = service["metadata"]["name"]
            namespace = service["metadata"]["namespace"]
            hosts.write("127.0.0.1 {}\n".format(name))
            hosts.write("127.0.0.1 {}.{}.svc.cluster.local\n".format(name, namespace))


def get_pod_name(deployment_name):
    """Given the deployment name, return the name of its pod."""
    pods = [line.split()[0] for line in
            str(check_output(["kubectl", "get", "pod"]), "utf-8").splitlines()]
    for pod in pods:
        if pod.startswith(deployment_name + "-"):
            return pod
    raise RuntimeError("Telepresence pod not found for Deployment '{}'.".format(
        deployment_name))

processes = []
pod_name = get_pod_name(argv[2])

# 1. write /etc/hosts
write_etc_hosts()
# 2. forward remote port to here:
processes.append(Popen([
    "nc", "-lk", "-p", "22", "-e", "/usr/local/bin/remote-ssh.sh", pod_name]))
for port_number in argv[3:]:
    processes.append(Popen([
        "sshpass", "-phello", "ssh", "-oStrictHostKeyChecking=no", "root@localhost",
        "-R", "{}:127.0.0.1:{}".format(port_number, port_number), "-N"]))

# 2. write k8s.env
setuid(int(argv[1]))
write_env(pod_name)
# 3. start proxies
for port in range(2000, 2020):
    # XXX need to map service name to port# somehow
    # XXX what if there is more than 20 services
    p = Popen(["kubectl", "port-forward", pod_name, str(port)])
    processes.append(p)
for p in processes:
    p.wait()
