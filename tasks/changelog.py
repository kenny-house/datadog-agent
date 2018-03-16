"""
Changelog tasks
"""
import uuid
import os
import subprocess
import pypandoc
import yaml

from invoke import task

@task
def new(ctx, title):
    if not os.path.exists(".changelog"):
        os.makedirs(".changelog")

    release_note_path = ".changelog/{}-{}.md".format(title, uuid.uuid4().hex[16:])
    release_note_file = open(release_note_path,"w+")
    release_note_file.close()

    if os.getenv('EDITOR') is None:
        print("Please edit {} manualy".format(release_note_path))
    else:
        os.system("{} {}".format(os.getenv('EDITOR'), release_note_path))
    print("When you are done editing don't forget to run 'inv changelog.done'")

@task
def done(ctx):
    for release_note in os.listdir(".changelog"):
        sections = {}
        current_section = None
        with open(".changelog/{}".format(release_note), "r") as release_note_file:
            for line in release_note_file:
                if line.startswith("# "):
                    current_section = line[2:].strip()
                    sections[current_section] = ""
                elif current_section is not None:
                    sections[current_section] += line
        for section in sections:
            sections[section] = [pypandoc.convert_text(sections[section], to='rst', format='markdown')]

        with open("releasenotes/notes/{}.yaml".format(release_note[:-3]), 'w') as outfile:
            yaml.safe_dump(sections, outfile, default_flow_style=False)
        os.remove(".changelog/{}".format(release_note))