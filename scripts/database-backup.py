#!/usr/bin/env python3
"""Use PostgreSQL's official clients; restore only into an empty, named database.

Connection configuration uses libpq PG* environment variables, never CLI URLs.
Restored credentials are revoked and active attempts fenced in the same transaction.
The archive contains sensitive operational data: keep it private and encrypted at rest.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import uuid

PG_ENV = ("PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
          "PGSSLMODE", "PGSSLROOTCERT", "PGSERVICE", "PGSERVICEFILE", "PGCONNECT_TIMEOUT")


def command(container, program, *args):
    prefix = []
    if container:
        prefix = ["docker", "exec", "-i"]
        for key in PG_ENV:
            if key in os.environ:
                prefix += ["--env", key]
        prefix += [container]
    return prefix + [program, *args]


def query(container, sql):
    result = subprocess.run(command(container, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql),
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=30)
    if result.returncode:
        raise RuntimeError("database preflight failed; check libpq configuration and permissions")
    return result.stdout.decode().strip()


def digest(stream):
    value = hashlib.sha256()
    while chunk := stream.read(1024 * 1024):
        value.update(chunk)
    return value.hexdigest()


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def backup(args):
    target = Path(args.directory).absolute()
    if target.exists():
        raise RuntimeError("backup destination already exists; refusing to overwrite")
    database = query(args.container, "SELECT current_database()")
    version = query(args.container, "SHOW server_version_num")
    tables = query(args.container, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('hosts','tasks','audit','projects','sessions','analyses')")
    if tables != "6":
        raise RuntimeError("source is not an initialized Sideria database")
    temp = Path(tempfile.mkdtemp(prefix=".sideria-backup-", dir=target.parent))
    try:
        archive = temp / "database.dump"
        with archive.open("xb") as stream:
            result = subprocess.run(command(args.container, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--lock-wait-timeout=10s"),
                                    stdout=stream, stderr=subprocess.DEVNULL, timeout=600)
            if result.returncode:
                raise RuntimeError("pg_dump failed; no complete backup published")
            stream.flush()
            os.fsync(stream.fileno())
        with archive.open("rb") as stream:
            checksum = digest(stream)
        manifest = {"format": 1, "id": str(uuid.uuid4()), "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    "database": database, "server_version_num": int(version), "archive": "database.dump",
                    "sha256": checksum, "bytes": archive.stat().st_size,
                    "scope": "PostgreSQL only; external configuration, keys, Agent journals and transfer spool excluded"}
        with (temp / "manifest.json").open("x") as stream:
            json.dump(manifest, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        sync_directory(temp)
        # The operator owns the destination parent; no existing bundle may be replaced.
        if target.exists():
            raise RuntimeError("backup destination appeared during creation")
        temp.rename(target)
        sync_directory(target.parent)
        print(json.dumps({"backup_id": manifest["id"], "bytes": manifest["bytes"], "sha256": checksum}))
    finally:
        if temp.exists():
            shutil.rmtree(temp)


EMPTY_GUARD = """
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema'
AND c.relkind IN ('r','p','S','v','m','f')) THEN
RAISE EXCEPTION 'restore target is not empty';
END IF; END $$;
"""


def restore(args):
    if not args.target:
        raise RuntimeError("restore requires --target naming the empty destination database")
    bundle = Path(args.directory)
    if (bundle / "manifest.json").stat().st_size > 16384:
        raise RuntimeError("manifest exceeds supported size")
    manifest = json.loads((bundle / "manifest.json").read_text())
    if manifest.get("format") != 1 or manifest.get("archive") != "database.dump":
        raise RuntimeError("unsupported backup format")
    backup_id = str(uuid.UUID(manifest["id"]))
    target = query(args.container, "SELECT current_database()")
    if target != args.target or target == manifest["database"]:
        raise RuntimeError("restore requires a different, explicitly named database")
    if int(query(args.container, "SHOW server_version_num")) // 10000 < manifest["server_version_num"] // 10000:
        raise RuntimeError("restore to an older PostgreSQL major version is unsupported")
    archive_fd = os.open(bundle / "database.dump", os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(archive_fd, "rb") as archive:
        info = os.fstat(archive.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size != manifest["bytes"] or digest(archive) != manifest["sha256"]:
            raise RuntimeError("backup checksum or size mismatch; destination untouched")
        archive.seek(0)
        # Keep restore and fencing in one transaction: a crash/error cannot publish
        # old live credentials or partially restored task facts.
        target_process = subprocess.Popen(command(args.container, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1"),
                                          stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        source_process = None
        watchdog = threading.Timer(600, target_process.kill)
        watchdog.start()
        try:
            target_process.stdin.write(("BEGIN;\n" + EMPTY_GUARD).encode())
            target_process.stdin.flush()
            source_process = subprocess.Popen(command(args.container, "pg_restore", "--file=-", "--no-owner", "--no-privileges", "--exit-on-error"),
                                              stdin=archive, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            shutil.copyfileobj(source_process.stdout, target_process.stdin, 1024 * 1024)
            if source_process.wait(timeout=600):
                raise RuntimeError("pg_restore failed; target transaction rolled back")
            fence = f"""
INSERT INTO public.audit(source,host_id,action,request_id,state)
SELECT 'restore',host_id,body->>'action',id,'uncertain' FROM public.tasks WHERE state IN ('pending','running','uncertain');
UPDATE public.tasks SET state='uncertain',updated_at=now(),
body=jsonb_set(jsonb_set(body,'{{state}}','"uncertain"'), '{{error}}', '"database restored; reconcile Agent journal and target before re-enrollment"')
WHERE state IN ('pending','running','uncertain');
UPDATE public.hosts SET revoked=true,token_hash=NULL,enrollment_hash=NULL,enrollment_expires=NULL,identity_generation=identity_generation+1;
DELETE FROM public.sessions;
INSERT INTO public.audit(source,host_id,action,request_id,state) VALUES('restore','','database.restore','{backup_id}','quarantined');
COMMIT;
"""
            target_process.stdin.write(fence.encode())
            target_process.stdin.close()
            if target_process.wait(timeout=600):
                raise RuntimeError("restore or quarantine failed; target transaction rolled back")
        finally:
            watchdog.cancel()
            if source_process is not None and source_process.poll() is None:
                source_process.kill()
                source_process.wait()
            if target_process.poll() is None:
                target_process.kill()  # Closing the connection rolls back the uncommitted transaction.
                target_process.wait()
    print(json.dumps({"backup_id": backup_id, "restored": True, "credentials_revoked": True, "active_attempts_quarantined": True}))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("backup", "restore"))
    parser.add_argument("directory")
    parser.add_argument("--container", help="Use official PostgreSQL clients in this authorized local test container")
    parser.add_argument("--target", help="Explicit empty destination database name for restore")
    args = parser.parse_args()
    try:
        (backup if args.operation == "backup" else restore)(args)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        # Never print commands, connection strings, SQL data or provider secrets.
        print(f"backup/restore failed: {type(error).__name__}: {error if isinstance(error, RuntimeError) else 'invalid input or client operation failed'}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
